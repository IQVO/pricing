#!/usr/bin/env python3
"""Assert this chart's Service selectors isolate each component.

Why this exists (fleet-wide): `app.kubernetes.io/name` + `instance` are identical
on every pod a release creates. A Service that selects on those two alone selects
ALL of them, which turns into a wrong answer the day a second component is added.
Every Deployment here carries `app.kubernetes.io/component` in selector.matchLabels
and its pod labels, and every Service selects on it, so each Service selects
EXACTLY ONE Deployment. This test fails if that ever stops being true.

pricing has one component by default, the api (REST, the outbox relay, the
offer-availability consumer and the clock-driven policy loop in one process),
and two optional read-only ones: the A2A agent (a2a.enabled, ADR 0007) and the
MCP server (mcp.enabled, ADR 0009). There is no frontend: the chart renders
nothing for binaries that do not exist. The checks run with every optional
template (HPA, Ingress, HTTPRoute, the A2A agent and its route, the MCP server
and its route) enabled together.

Run: python3 charts/pricing/tests/test_service_selectors.py
Needs: helm, PyYAML.
"""

from __future__ import annotations

import subprocess
import sys
from pathlib import Path

CHART_DIR = Path(__file__).resolve().parents[1]
RELEASE = "pricing"
A2A = f"{RELEASE}-a2a"
MCP = f"{RELEASE}-mcp"

# Dummy values, no password: the chart refuses to render without a database
# source, the Kafka brokers and the offer-availability consumer group. None is
# ever dialled.
GROUPS = {
    "offerAvailabilityConsumerGroup": "pricing-offer-availability",
}


def runtime(skip: str | None = None) -> list[str]:
    """The mandatory values, minus the one named by skip (database, kafka or a group key)."""
    args: list[str] = []
    if skip != "database":
        args += ["--set", "database.url=postgres://u@example.invalid:5432/db"]
    if skip != "kafka":
        args += ["--set", "kafka.brokers=kafka.example.invalid:9092"]
    for key, value in GROUPS.items():
        if skip != key:
            args += ["--set", f"config.{key}={value}"]
    return args


BASE = runtime()
ENABLE_EVERYTHING = BASE + [
    "--set", "autoscaling.api.enabled=true",
    "--set", "gatewayApi.enabled=true",
    "--set", "ingress.enabled=true",
    "--set", "a2a.enabled=true",
    "--set", "a2a.route.enabled=true",
    "--set", "a2a.route.parentRefs[0].name=warehouse-gateway",
    "--set", "a2a.route.parentRefs[0].namespace=kong",
    "--set", "mcp.enabled=true",
    "--set", "mcp.route.enabled=true",
    "--set", "mcp.route.parentRefs[0].name=warehouse-gateway",
    "--set", "mcp.route.parentRefs[0].namespace=kong",
    "--set", "config.currency=USD",
    "--set", "config.policyInterval=2s",
    "--set", "config.outboxRelayInterval=2s",
    "--set", "config.shutdownDrainDelay=5s",
    "--set", "database.migrationsUrl=postgres://u@example.invalid:5432/direct",
]

# Every variable cmd/api reads, with a dedicated chart value.
API_ENV = (
    "HTTP_ADDR", "LOG_LEVEL", "DATABASE_URL", "MIGRATIONS_DATABASE_URL", "KAFKA_BROKERS",
    "OFFER_AVAILABILITY_CONSUMER_GROUP", "PRICING_CURRENCY", "POLICY_INTERVAL",
    "OUTBOX_RELAY_INTERVAL", "OUTBOX_MAX_ATTEMPTS", "SHUTDOWN_DRAIN_DELAY",
)


# What the A2A agent (cmd/a2a) must render, and what must never reach it.
A2A_ENV = ("A2A_ADDR", "A2A_AGENT_URL", "DATABASE_URL", "MIGRATIONS_DATABASE_URL", "LOG_LEVEL")
A2A_FORBIDDEN_ENV = {
    "KAFKA_BROKERS", "EVENT_PUBLISHER", "OUTBOX_RELAY_INTERVAL", "OUTBOX_MAX_ATTEMPTS",
    "OFFER_AVAILABILITY_CONSUMER_GROUP", "POLICY_INTERVAL", "PRICING_CURRENCY", "HTTP_ADDR", "SHUTDOWN_DRAIN_DELAY",
}

# What the MCP server (cmd/mcp) must render, and what must never reach it.
MCP_ENV = ("MCP_ADDR", "DATABASE_URL", "MIGRATIONS_DATABASE_URL", "LOG_LEVEL")
MCP_FORBIDDEN_ENV = A2A_FORBIDDEN_ENV | {"A2A_ADDR", "A2A_AGENT_URL"}


def helm_template(extra_args: list[str]) -> subprocess.CompletedProcess:
    return subprocess.run(
        ["helm", "template", RELEASE, str(CHART_DIR), *extra_args],
        capture_output=True, text=True,
    )


def render(extra_args: list[str]) -> list[dict]:
    result = helm_template(extra_args)
    if result.returncode != 0:
        raise SystemExit(f"FAIL: helm template {' '.join(extra_args)}:\n{result.stderr}")
    try:
        import yaml  # type: ignore
    except ModuleNotFoundError:  # pragma: no cover - environment guard
        print("SKIP: PyYAML not available; cannot assert selectors", file=sys.stderr)
        raise SystemExit(0)
    return [d for d in yaml.safe_load_all(result.stdout) if d]


def selector_of(doc: dict) -> dict:
    return doc.get("spec", {}).get("selector") or {}


def pod_labels_of(doc: dict) -> dict:
    return doc.get("spec", {}).get("template", {}).get("metadata", {}).get("labels") or {}


def matches(selector: dict, labels: dict) -> bool:
    return bool(selector) and all(labels.get(k) == v for k, v in selector.items())


def env_of(dep: dict) -> list[dict]:
    return dep["spec"]["template"]["spec"]["containers"][0].get("env", [])


def env_names(dep: dict) -> list[str]:
    return [e["name"] for e in env_of(dep)]


def env_value(dep: dict, name: str) -> str | None:
    return next((e.get("value") for e in env_of(dep) if e["name"] == name), None)


def check_components(docs: list[dict], failures: list[str]) -> None:
    services = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "Service"}
    deployments = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "Deployment"}

    if set(deployments) != {RELEASE, A2A, MCP}:
        failures.append(f"the chart has the api, the a2a and the mcp Deployments, rendered: {sorted(deployments)}")
    if set(services) != {RELEASE, A2A, MCP}:
        failures.append(f"the chart has the api, the a2a and the mcp Services, rendered: {sorted(services)}")
    for name, component in ((RELEASE, "api"), (A2A, "a2a"), (MCP, "mcp")):
        if name not in services:
            failures.append(f"the {component} Service was not rendered")
        elif selector_of(services[name]).get("app.kubernetes.io/component") != component:
            failures.append(f"the {component} Service selector must pin component={component}")

    # Every Deployment's own selector must pin a component too, and be
    # satisfied by its pod labels.
    for name, dep in deployments.items():
        match_labels = dep["spec"]["selector"].get("matchLabels") or {}
        if "app.kubernetes.io/component" not in match_labels:
            failures.append(f"Deployment {name} selector.matchLabels lacks app.kubernetes.io/component")
        if not matches(match_labels, pod_labels_of(dep)):
            failures.append(f"Deployment {name} pod labels do not satisfy its own selector")

    # The real invariant: each Service selects exactly one Deployment.
    for svc_name, svc in services.items():
        sel = selector_of(svc)
        hit = [d for d, dep in deployments.items() if matches(sel, pod_labels_of(dep))]
        if len(hit) != 1:
            failures.append(f"Service {svc_name} selects {len(hit)} Deployments {sorted(hit)}; expected exactly 1")

    # Ingress and the api HTTPRoute route to the api Service only; the a2a and
    # mcp HTTPRoutes route to their own Service only; the HPA owns the api replicas.
    for d in docs:
        if d.get("kind") == "Ingress":
            backends = {p["backend"]["service"]["name"] for r in d["spec"]["rules"] for p in r["http"]["paths"]}
            if backends != {RELEASE}:
                failures.append(f"the Ingress must route to the api Service only, got {sorted(backends)}")
        if d.get("kind") == "HTTPRoute":
            backends = {b["name"] for r in d["spec"]["rules"] for b in r["backendRefs"]}
            want = {d["metadata"]["name"]} if d["metadata"]["name"] in (A2A, MCP) else {RELEASE}
            if backends != want:
                failures.append(f"the HTTPRoute {d['metadata']['name']} must route to {sorted(want)} only, got {sorted(backends)}")
    hpa = next((d for d in docs if d.get("kind") == "HorizontalPodAutoscaler"), None)
    if hpa is None or hpa["spec"]["scaleTargetRef"]["name"] != RELEASE:
        failures.append("the api HPA was not rendered (or does not scale the api Deployment)")
    elif "replicas" in deployments.get(RELEASE, {}).get("spec", {}):
        failures.append("the api Deployment must omit replicas when its HPA owns them")

    check_a2a(deployments.get(A2A), failures)
    check_mcp(deployments.get(MCP), failures)

    api = deployments.get(RELEASE)
    if api is None:
        failures.append("the api Deployment was not rendered")
        return
    api_env = env_names(api)
    for want in API_ENV:
        if want not in api_env:
            failures.append(f"the api Deployment does not render {want} from its dedicated value")
    duplicates = sorted({n for n in api_env if api_env.count(n) > 1})
    if duplicates:
        failures.append(f"the api Deployment renders env vars twice: {duplicates}")
    for name, want in (("PRICING_CURRENCY", "USD"), ("POLICY_INTERVAL", "2s"),
                       ("OUTBOX_RELAY_INTERVAL", "2s"), ("SHUTDOWN_DRAIN_DELAY", "5s")):
        if env_value(api, name) != want:
            failures.append(f"the api Deployment renders {name}={env_value(api, name)!r}, want {want!r}")


def check_a2a(dep: dict | None, failures: list[str]) -> None:
    """The A2A agent is read-only: Postgres only, no Kafka/outbox/consumer variable."""
    if dep is None:
        failures.append("the a2a Deployment was not rendered")
        return
    names = env_names(dep)
    for want in A2A_ENV:
        if want not in names:
            failures.append(f"the a2a Deployment does not render {want}")
    stray = sorted(set(names) & A2A_FORBIDDEN_ENV)
    if stray:
        failures.append(f"the read-only a2a Deployment renders {stray}")
    if [n for n in names if n.endswith("_CONSUMER_GROUP") or "KAFKA" in n or "OUTBOX" in n]:
        failures.append("the read-only a2a Deployment renders a Kafka/outbox/consumer variable")
    container = dep["spec"]["template"]["spec"]["containers"][0]
    if container.get("command") != ["/app/a2a"]:
        failures.append("the a2a Deployment must run /app/a2a")
    if env_value(dep, "A2A_ADDR") != ":8093":
        failures.append(f"the a2a agent must listen on :8093, renders {env_value(dep, 'A2A_ADDR')!r}")
    if "envFrom" in container:
        failures.append("the a2a Deployment must not mount the api ConfigMap")


def check_mcp(dep: dict | None, failures: list[str]) -> None:
    """The MCP server is read-only: Postgres only, no Kafka/outbox/consumer variable."""
    if dep is None:
        failures.append("the mcp Deployment was not rendered")
        return
    names = env_names(dep)
    for want in MCP_ENV:
        if want not in names:
            failures.append(f"the mcp Deployment does not render {want}")
    stray = sorted(set(names) & MCP_FORBIDDEN_ENV)
    if stray:
        failures.append(f"the read-only mcp Deployment renders {stray}")
    if [n for n in names if n.endswith("_CONSUMER_GROUP") or "KAFKA" in n or "OUTBOX" in n]:
        failures.append("the read-only mcp Deployment renders a Kafka/outbox/consumer variable")
    container = dep["spec"]["template"]["spec"]["containers"][0]
    if container.get("command") != ["/app/mcp"]:
        failures.append("the mcp Deployment must run /app/mcp")
    if env_value(dep, "MCP_ADDR") != ":8090":
        failures.append(f"the mcp server must listen on :8090, renders {env_value(dep, 'MCP_ADDR')!r}")
    if "envFrom" in container:
        failures.append("the mcp Deployment must not mount the api ConfigMap")


def check_refusals(failures: list[str]) -> None:
    cases: list[tuple[str, list[str], str]] = [
        ("without a database source", runtime("database"), "requires database.url or database.existingSecret"),
        ("without kafka.brokers", runtime("kafka"), "kafka.brokers"),
        ("a *_MODE through extraEnv",
         BASE + ["--set", "extraEnv[0].name=CACHE_MODE", "--set", "extraEnv[0].value=permissive"], "extraEnv sets CACHE_MODE"),
        ("a consumer group through extraEnv",
         BASE + ["--set", "extraEnv[0].name=OFFER_AVAILABILITY_CONSUMER_GROUP", "--set", "extraEnv[0].value=pod"],
         "extraEnv sets OFFER_AVAILABILITY_CONSUMER_GROUP"),
        ("a chart-owned variable through extraEnv",
         BASE + ["--set", "extraEnv[0].name=KAFKA_BROKERS", "--set", "extraEnv[0].value=x:9092"], "extraEnv sets KAFKA_BROKERS"),
        ("a grace period under the shutdown bounds", BASE + ["--set", "terminationGracePeriodSeconds=40"], "terminationGracePeriodSeconds"),
        ("a grace period equal to the shutdown bounds", BASE + ["--set", "terminationGracePeriodSeconds=45"], "must exceed 45"),
        ("an http address on another port than the container's", BASE + ["--set", "config.httpAddr=:9090"], "config.httpAddr"),
        ("an a2a address on another port than the a2a container's",
         BASE + ["--set", "a2a.enabled=true", "--set", "a2a.httpAddr=:9090"], "a2a.httpAddr"),
        ("an mcp address on another port than the mcp container's",
         BASE + ["--set", "mcp.enabled=true", "--set", "mcp.httpAddr=:9090"], "mcp.httpAddr"),
    ]
    for key in GROUPS:
        cases.append((f"without config.{key}", runtime(key), f"config.{key} is required"))
    for label, args, needle in cases:
        result = helm_template(args)
        if result.returncode == 0 or needle not in result.stderr:
            failures.append(f"chart rendered (or failed for another reason) {label}")


def main() -> int:
    failures: list[str] = []

    check_components(render(ENABLE_EVERYTHING), failures)

    # Default values must not deploy an HPA, a route, the A2A agent or the MCP server, nor any optional env.
    defaults = render(BASE)
    stray = [d["kind"] for d in defaults if d.get("kind") in {"HorizontalPodAutoscaler", "Ingress", "HTTPRoute"}]
    if stray:
        failures.append(f"optional templates rendered with default values: {stray}")
    optional_defaults = [d["kind"] for d in defaults if d["metadata"]["name"] in (A2A, MCP)]
    if optional_defaults:
        failures.append(f"the a2a agent or the mcp server rendered with default values: {optional_defaults}")
    dep = next(d for d in defaults if d.get("kind") == "Deployment")
    names = env_names(dep)
    off = {"PRICING_CURRENCY", "POLICY_INTERVAL", "OUTBOX_RELAY_INTERVAL", "SHUTDOWN_DRAIN_DELAY"}
    if off & set(names):
        failures.append(f"these must be off with default values: {sorted(off & set(names))}")
    for want in ("KAFKA_BROKERS", "OUTBOX_MAX_ATTEMPTS", *(k for k in API_ENV if k.endswith("_CONSUMER_GROUP"))):
        if want not in names:
            failures.append(f"{want} is mandatory and must always be rendered")

    check_refusals(failures)

    if failures:
        for f in failures:
            print(f"FAIL: {f}")
        return 1

    print("PASS: each Service selects exactly one Deployment (the api; the read-only a2a agent and mcp server when enabled); the HPA, Ingress, "
          "HTTPRoute, a2a agent and mcp server are off by default, and each route targets its own Service; every cmd/api env var has a dedicated value; the chart refuses to render without a "
          "database source, brokers or the consumer group, with a *_MODE or a consumer group in extraEnv, with a grace "
          "period that does not exceed the shutdown bounds, or with an http address off the container port")
    return 0


def agents_colocated() -> int:
    comp = "app.kubernetes.io/component"
    failures: list[str] = []
    # the every-component render that has both mcp and a2a (ENABLE_EVERYTHING, or ENABLE_ALL_COMPONENTS
    # where ENABLE_EVERYTHING covers only the api); object names come from the render (the helm release
    # name may differ from the chart name)
    full = None
    presets = [ENABLE_EVERYTHING] + ([globals()["ENABLE_ALL_COMPONENTS"]] if "ENABLE_ALL_COMPONENTS" in globals() else [])
    presets += [BASE + ["--set", "mcp.enabled=true", "--set", "a2a.enabled=true"]]  # retail/transport shape: no global all-components render
    for args in presets:
        try:
            off = render(args)
        except SystemExit:
            continue
        names = {d["metadata"]["name"] for d in off if d.get("kind") == "Deployment"}
        mcp = sorted(n for n in names if n.endswith("-mcp") and n[:-4] + "-a2a" in names)
        if mcp:
            full = mcp[0][:-4]
            break
    if full is None:
        print("FAIL: no every-component render has both the mcp and a2a Deployments")
        return 1
    agents = f"{full}-agents"
    docs = render(args + ["--set", "agents.colocate=true"])
    services = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "Service"}
    deployments = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "Deployment"}
    off_deps = {d["metadata"]["name"] for d in off if d.get("kind") == "Deployment"}
    want_deps = (off_deps - {f"{full}-mcp", f"{full}-a2a"}) | {agents}
    if set(deployments) != want_deps:
        failures.append(f"with agents.colocate: Deployments {sorted(deployments)}, want {sorted(want_deps)}")
    if set(services) != {d["metadata"]["name"] for d in off if d.get("kind") == "Service"}:
        failures.append("with agents.colocate: the set of Services changed")
    for name, svc in services.items():
        hit = [d for d, dep in deployments.items() if matches(selector_of(svc), pod_labels_of(dep))]
        want = [agents] if name in (f"{full}-mcp", f"{full}-a2a") else None
        if len(hit) != 1 or (want and hit != want):
            failures.append(f"with agents.colocate: Service {name} selects {sorted(hit)}, want {want or 'exactly one'}")
    dep = deployments.get(agents)
    if dep is not None:
        if dep["spec"]["selector"].get("matchLabels", {}).get(comp) != "agents":
            failures.append("the agents Deployment selector.matchLabels must pin component=agents")
        names = sorted(c["name"] for c in dep["spec"]["template"]["spec"]["containers"])
        if names != ["a2a", "mcp"]:
            failures.append(f"the agents Deployment must run the containers a2a and mcp, got {names}")
    for f in failures:
        print(f"FAIL: {f}")
    if not failures:
        print(f"PASS: agents.colocate: {len(services)} Services, mcp + a2a both select {agents} (containers mcp, a2a)")
    return 1 if failures else 0


if __name__ == "__main__":
    raise SystemExit(main() or agents_colocated())
