-- ADR 0010: CONSOLIDATED is a known service tier. A schedule may declare a row
-- for it (optional) and a quote group may carry it; the CHECKs name it.
ALTER TABLE tier_charges DROP CONSTRAINT tier_charges_tier_check;
ALTER TABLE tier_charges ADD CONSTRAINT tier_charges_tier_check
    CHECK (tier IN ('SAME_DAY', 'NEXT_DAY', 'TWO_DAY', 'STANDARD', 'CONSOLIDATED'));

ALTER TABLE quote_groups DROP CONSTRAINT quote_groups_tier_check;
ALTER TABLE quote_groups ADD CONSTRAINT quote_groups_tier_check
    CHECK (tier IN ('SAME_DAY', 'NEXT_DAY', 'TWO_DAY', 'STANDARD', 'CONSOLIDATED'));
