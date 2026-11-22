# Equivalent mutants (gremlins v0.6.0, `make mutation`, measured 2026-11-15)

These mutants survive because the mutation does not change any observable
behaviour. They are not silenced in `.gremlins.yaml`; the efficacy gate (95)
sits just below the lowest package that carries them.

| Package | File:line | Mutator | Why it is equivalent |
| --- | --- | --- | --- |
| itemprice | `itemprice.go:155:44` (`effective`) | CONDITIONALS_BOUNDARY (`<` to `<=`) | A sale price is always strictly below the list price (`ScheduleSale`, `SetListPrice` and `Restore` refuse `>=`) and windows never overlap, so the comparison against `best` never sees equal amounts. |
| itemprice | `itemprice.go:165:44` (`saleEnd`) | CONDITIONALS_BOUNDARY (`<` to `<=`) | Same invariant: no active sale has a price equal to the list price. |
| tax | `tax.go:152:25` (`orderBands` sort comparator) | CONDITIONALS_BOUNDARY (`>` to `>=`) | For equal starts the comparator returns 1 instead of 0; `slices.SortFunc` only tests `cmp < 0`, so the order is the same. |
| tax | `tax.go:152:25` (`orderBands` sort comparator) | CONDITIONALS_NEGATION (`>` to `<=`) | Same: it turns the `0` result for equal starts into `1`, and a non-negative result never moves an element. |
