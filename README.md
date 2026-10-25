# pricing

Storefront (e-commerce) domain of the retail-network organisation, bounded context **Pricing** (Core, storefront tier).

Owns what a shopper pays: the list price and sale windows per catalogue item, the delivery charge per service tier, a synthetic tax table, and the binding, immutable, expiring PriceQuote a checkout session is confirmed against. One configured currency, integer minor units; promotions are a later phase (D10).

Status: under construction. The design lives in [warehouse-docs](https://iqvo.github.io/warehouse-docs/storefront/) `storefront/model/model.yaml` (single source of truth).
This repository is part of the storefront domain: it consumes the retail network's Open Host Service routes and its
published `retailnetwork.*` events; it never calls an in-org or restricted network route.

Runtime: Go, hexagonal, real PostgreSQL and Kafka only (`DATABASE_URL`, `KAFKA_BROKERS` are mandatory), CloudEvents 1.0 on every message
(`com.retailnetwork.storefront.pricing.<entity>.<Event>`), transactional outbox.

## Disclaimer

This project is a domain-driven design study. It is not associated with, endorsed by or affiliated with any company, and it uses no company's code, data, logos or trademarks. All names, figures and examples are illustrative or synthetic.

See `docs/adr/` for decisions and `docs/before-first-release.md` for the packaging checklist.
