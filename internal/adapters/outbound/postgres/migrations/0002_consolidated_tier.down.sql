-- Restores the four classic tiers. It fails (loudly, nothing is deleted) while a
-- tier_charges or quote_groups row still carries CONSOLIDATED.
ALTER TABLE quote_groups DROP CONSTRAINT quote_groups_tier_check;
ALTER TABLE quote_groups ADD CONSTRAINT quote_groups_tier_check
    CHECK (tier IN ('SAME_DAY', 'NEXT_DAY', 'TWO_DAY', 'STANDARD'));

ALTER TABLE tier_charges DROP CONSTRAINT tier_charges_tier_check;
ALTER TABLE tier_charges ADD CONSTRAINT tier_charges_tier_check
    CHECK (tier IN ('SAME_DAY', 'NEXT_DAY', 'TWO_DAY', 'STANDARD'));
