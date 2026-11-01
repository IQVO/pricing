---
id: entity-relationship
title: Entity Relationship
sidebar_label: Entity Relationship
description: "The Postgres schema of Pricing: every table of its migration, with keys, relationships and the outbox, inbox and idempotency tables."
---

# Entity Relationship

The schema: [`0001_pricing_schema.up.sql`](https://github.com/IQVO/pricing/blob/develop/internal/adapters/outbound/postgres/migrations/0001_pricing_schema.up.sql), embedded in the binary and applied at boot with golang-migrate (which adds its own `schema_migrations` table). Pricing owns its database and shares no table with another context.

13 tables. Key markers: PK primary key, FK foreign key, UK unique. Types are the column types of the migration; constraints (CHECK, defaults, partial indexes) are in the file.

```mermaid
erDiagram
    item_prices {
        text catalog_item_id PK
        text state
        char currency
        bigint list_price_minor
        bigint version
        timestamptz evaluated_at
        bigint published_list_minor
        bigint published_effective_minor
        timestamptz published_sale_ends_at
        timestamptz next_boundary_at
    }
    sale_windows {
        text catalog_item_id PK,FK
        text sale_id PK
        bigint price_minor
        timestamptz starts_at
        timestamptz ends_at
    }
    delivery_charge_schedules {
        text schedule_id PK
        integer version UK
        text state
        timestamptz published_at
    }
    tier_charges {
        text schedule_id PK,FK
        text tier PK
        char currency
        bigint charge_minor
        bigint free_over_minor
    }
    tax_tables {
        text tax_table_id PK
        integer version UK
        text state
        timestamptz published_at
    }
    tax_bands {
        text tax_table_id PK,FK
        char postal_prefix_from PK
        char postal_prefix_to
        integer rate_basis_points
    }
    price_quotes {
        text quote_id PK
        text cart_id
        text state
        char currency
        bigint tax_minor
        bigint total_minor
        integer schedule_version
        integer tax_table_version
        timestamptz issued_at
        timestamptz expires_at
        bigint version
    }
    quote_lines {
        text quote_id PK,FK
        integer line_no PK
        text catalog_item_id
        integer quantity
        bigint unit_price_minor
        bigint line_total_minor
        bigint price_version
    }
    quote_groups {
        text quote_id PK,FK
        integer group_no PK
        text tier
        bigint delivery_charge_minor
    }
    catalog_items {
        text catalog_item_id PK
        text state
        timestamptz updated_at
    }
    processed_events {
        text event_id PK
        text event_type
        timestamptz processed_at
    }
    idempotency_keys {
        text route PK
        text key PK
        text request_hash
        integer status
        text content_type
        bytea body
        timestamptz created_at
    }
    outbox_events {
        bigserial id PK
        text event_id UK
        text topic
        text event_type
        text subject
        bytea key
        text dataschema
        bytea value
        jsonb headers
        timestamptz created_at
        timestamptz published_at
        integer attempts
        text last_error
    }
    item_prices ||--o{ sale_windows : "catalog_item_id"
    delivery_charge_schedules ||--o{ tier_charges : "schedule_id"
    tax_tables ||--o{ tax_bands : "tax_table_id"
    price_quotes ||--o{ quote_lines : "quote_id"
    price_quotes ||--o{ quote_groups : "quote_id"
```

Source: `internal/adapters/outbound/postgres/migrations/0001_pricing_schema.up.sql`.

## Tables

| Table | Columns | Primary key | Role |
| --- | --- | --- | --- |
| `item_prices` | 10 | `catalog_item_id` | aggregate or read-model state |
| `sale_windows` | 5 | `catalog_item_id, sale_id` | aggregate or read-model state; FK to `item_prices` |
| `delivery_charge_schedules` | 4 | `schedule_id` | aggregate or read-model state |
| `tier_charges` | 5 | `schedule_id, tier` | aggregate or read-model state; FK to `delivery_charge_schedules` |
| `tax_tables` | 4 | `tax_table_id` | aggregate or read-model state |
| `tax_bands` | 4 | `tax_table_id, postal_prefix_from` | aggregate or read-model state; FK to `tax_tables` |
| `price_quotes` | 11 | `quote_id` | aggregate or read-model state |
| `quote_lines` | 7 | `quote_id, line_no` | aggregate or read-model state; FK to `price_quotes` |
| `quote_groups` | 4 | `quote_id, group_no` | aggregate or read-model state; FK to `price_quotes` |
| `catalog_items` | 3 | `catalog_item_id` | aggregate or read-model state |
| `processed_events` | 3 | `event_id` | inbox: one row per consumed CloudEvents id (dedupe) |
| `idempotency_keys` | 7 | `route, key` | stored REST answers per `Idempotency-Key`; the key is global, so `route` is always the empty string (kept to avoid a migration) and the row commits with the change and its outbox rows |
| `outbox_events` | 13 | `id` | transactional outbox the relay publishes to Kafka |
