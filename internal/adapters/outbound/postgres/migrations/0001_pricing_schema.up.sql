-- pricing schema (ADR 0002). One configured currency; money is integer minor units.

CREATE TABLE item_prices (
    catalog_item_id           text        PRIMARY KEY,
    state                     text        NOT NULL CHECK (state IN ('ACTIVE', 'WITHDRAWN')),
    currency                  char(3)     NOT NULL,
    list_price_minor          bigint      NOT NULL CHECK (list_price_minor >= 0),
    version                   bigint      NOT NULL CHECK (version >= 1),
    evaluated_at              timestamptz NOT NULL,
    published_list_minor      bigint      NOT NULL,
    published_effective_minor bigint      NOT NULL,
    published_sale_ends_at    timestamptz,
    -- the next sale-window start/end after evaluated_at: the boundary scan index.
    next_boundary_at          timestamptz
);
CREATE INDEX item_prices_boundary_idx ON item_prices (next_boundary_at) WHERE next_boundary_at IS NOT NULL;

CREATE TABLE sale_windows (
    catalog_item_id text        NOT NULL REFERENCES item_prices (catalog_item_id) ON DELETE CASCADE,
    sale_id         text        NOT NULL,
    price_minor     bigint      NOT NULL CHECK (price_minor >= 0),
    starts_at       timestamptz NOT NULL,
    ends_at         timestamptz NOT NULL,
    PRIMARY KEY (catalog_item_id, sale_id),
    CHECK (ends_at > starts_at)
);

CREATE TABLE delivery_charge_schedules (
    schedule_id  text        PRIMARY KEY,
    version      integer     NOT NULL UNIQUE CHECK (version >= 1),
    state        text        NOT NULL CHECK (state IN ('CURRENT', 'SUPERSEDED')),
    published_at timestamptz NOT NULL
);
-- exactly one schedule is CURRENT.
CREATE UNIQUE INDEX delivery_charge_schedules_one_current ON delivery_charge_schedules (state) WHERE state = 'CURRENT';

CREATE TABLE tier_charges (
    schedule_id     text    NOT NULL REFERENCES delivery_charge_schedules (schedule_id) ON DELETE CASCADE,
    tier            text    NOT NULL CHECK (tier IN ('SAME_DAY', 'NEXT_DAY', 'TWO_DAY', 'STANDARD')),
    currency        char(3) NOT NULL,
    charge_minor    bigint  NOT NULL CHECK (charge_minor >= 0),
    free_over_minor bigint  CHECK (free_over_minor >= 0),
    PRIMARY KEY (schedule_id, tier)
);

CREATE TABLE tax_tables (
    tax_table_id text        PRIMARY KEY,
    version      integer     NOT NULL UNIQUE CHECK (version >= 1),
    state        text        NOT NULL CHECK (state IN ('CURRENT', 'SUPERSEDED')),
    published_at timestamptz NOT NULL
);
-- exactly one table is CURRENT.
CREATE UNIQUE INDEX tax_tables_one_current ON tax_tables (state) WHERE state = 'CURRENT';

CREATE TABLE tax_bands (
    tax_table_id       text    NOT NULL REFERENCES tax_tables (tax_table_id) ON DELETE CASCADE,
    postal_prefix_from char(3) NOT NULL,
    postal_prefix_to   char(3) NOT NULL,
    rate_basis_points  integer NOT NULL CHECK (rate_basis_points BETWEEN 0 AND 10000),
    PRIMARY KEY (tax_table_id, postal_prefix_from),
    CHECK (postal_prefix_from <= postal_prefix_to)
);

CREATE TABLE price_quotes (
    quote_id           text        PRIMARY KEY,
    cart_id            text        NOT NULL,
    state              text        NOT NULL CHECK (state IN ('ISSUED', 'EXPIRED')),
    currency           char(3)     NOT NULL,
    tax_minor          bigint      NOT NULL CHECK (tax_minor >= 0),
    total_minor        bigint      NOT NULL CHECK (total_minor >= 0),
    schedule_version   integer     NOT NULL,
    tax_table_version  integer     NOT NULL,
    issued_at          timestamptz NOT NULL,
    expires_at         timestamptz NOT NULL,
    version            bigint      NOT NULL CHECK (version >= 1)
);
CREATE INDEX price_quotes_expiry_idx ON price_quotes (expires_at) WHERE state = 'ISSUED';
CREATE INDEX price_quotes_cart_idx ON price_quotes (cart_id);

CREATE TABLE quote_lines (
    quote_id         text   NOT NULL REFERENCES price_quotes (quote_id) ON DELETE CASCADE,
    line_no          integer NOT NULL,
    catalog_item_id  text   NOT NULL,
    quantity         integer NOT NULL CHECK (quantity >= 1),
    unit_price_minor bigint NOT NULL CHECK (unit_price_minor >= 0),
    line_total_minor bigint NOT NULL CHECK (line_total_minor >= 0),
    price_version    bigint NOT NULL,
    PRIMARY KEY (quote_id, line_no)
);

CREATE TABLE quote_groups (
    quote_id              text    NOT NULL REFERENCES price_quotes (quote_id) ON DELETE CASCADE,
    group_no              integer NOT NULL,
    tier                  text    NOT NULL CHECK (tier IN ('SAME_DAY', 'NEXT_DAY', 'TWO_DAY', 'STANDARD')),
    delivery_charge_minor bigint  NOT NULL CHECK (delivery_charge_minor >= 0),
    PRIMARY KEY (quote_id, group_no)
);

-- CatalogItemView: the read model fed by offer-availability events.
CREATE TABLE catalog_items (
    catalog_item_id text        PRIMARY KEY,
    state           text        NOT NULL CHECK (state IN ('ACTIVE', 'DISCONTINUED')),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE processed_events (
    event_id     text        PRIMARY KEY,
    event_type   text        NOT NULL,
    processed_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE idempotency_keys (
    route        text        NOT NULL,
    key          text        NOT NULL,
    request_hash text        NOT NULL,
    status       integer     NOT NULL,
    content_type text        NOT NULL,
    body         bytea       NOT NULL,
    created_at   timestamptz NOT NULL,
    PRIMARY KEY (route, key)
);
CREATE INDEX idempotency_keys_created_idx ON idempotency_keys (created_at);

CREATE TABLE outbox_events (
    id           bigserial   PRIMARY KEY,
    event_id     text        NOT NULL UNIQUE,
    topic        text        NOT NULL,
    event_type   text        NOT NULL,
    subject      text        NOT NULL,
    key          bytea       NOT NULL,
    dataschema   text        NOT NULL,
    value        bytea       NOT NULL,
    headers      jsonb       NOT NULL DEFAULT '[]'::jsonb,
    created_at   timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz,
    attempts     integer     NOT NULL DEFAULT 0,
    last_error   text
);
CREATE INDEX outbox_events_unpublished_idx ON outbox_events (id) WHERE published_at IS NULL;
