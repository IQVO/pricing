# quote_pricing_rules.feature — wave 3: how IssuePriceQuote prices a checkout
# (use-cases IssuePriceQuote, aggregate-design-canvas PriceQuote, ADR 0002 A6, A10,
# A11 and A14, troubleshooting). Arithmetic: line = unit x quantity; a group pays
# its tier's charge unless its own goods reach the tier's free-over threshold;
# tax = band rate x (goods + delivery) rounded half up; total = goods + delivery + tax.
Feature: A price quote prices exactly what checkout froze
  Background:
    Given the item "sku-1" is priced at 1000
    And the item "sku-2" is priced at 300
    And the item "sku-big" is priced at 20000
    And the item "sku-edge" is priced at 19999
    And the API accepts a POST to "/delivery-charge-schedules" with the body:
      """
      {"version":1,"tiers":[
        {"tier":"SAME_DAY","charge":{"amountMinor":2490,"currency":"BRL"}},
        {"tier":"NEXT_DAY","charge":{"amountMinor":1490,"currency":"BRL"},"freeOver":{"amountMinor":20000,"currency":"BRL"}},
        {"tier":"TWO_DAY","charge":{"amountMinor":990,"currency":"BRL"}},
        {"tier":"STANDARD","charge":{"amountMinor":590,"currency":"BRL"},"freeOver":{"amountMinor":10000,"currency":"BRL"}}]}
      """

  Scenario: Each group pays the charge of the tier it selected and tax covers goods plus delivery
    Given a tax rate of 1000 basis points everywhere from version 1
    When the API posts to "/price-quotes" the body:
      """
      {"cartId":"cart-1","destinationPostalPrefix":"787",
       "lines":[{"catalogItemId":"sku-1","quantity":1},{"catalogItemId":"sku-2","quantity":1}],
       "groups":[{"groupNo":1,"offerId":"offer-1","tier":"STANDARD","lineNos":[1]},
                 {"groupNo":2,"offerId":"offer-2","tier":"SAME_DAY","lineNos":[2]}]}
      """
    Then the API answers 201
    And the response field "groups.0.tier" is "STANDARD"
    And the response field "groups.0.deliveryCharge.amountMinor" is 590
    And the response field "groups.1.tier" is "SAME_DAY"
    And the response field "groups.1.deliveryCharge.amountMinor" is 2490
    And the response field "taxMinor" is 438
    And the response field "totalMinor" is 4818

  Scenario Outline: A group whose goods reach the tier's free-over threshold ships free
    Given a tax rate of 0 basis points everywhere from version 1
    When the API posts to "/price-quotes" the body:
      """
      {"cartId":"cart-1","destinationPostalPrefix":"787",
       "lines":[{"catalogItemId":"<item>","quantity":1}],
       "groups":[{"groupNo":1,"offerId":"offer-1","tier":"NEXT_DAY","lineNos":[1]}]}
      """
    Then the API answers 201
    And the response field "groups.0.deliveryCharge.amountMinor" is <charge>
    And the response field "totalMinor" is <total>

    Examples: goods exactly at the threshold, one minor unit below it
      | item     | charge | total |
      | sku-big  | 0      | 20000 |
      | sku-edge | 1490   | 21489 |

  Scenario: A tier without a free-over threshold charges however large the goods are
    Given a tax rate of 0 basis points everywhere from version 1
    When the API posts to "/price-quotes" the body:
      """
      {"cartId":"cart-1","destinationPostalPrefix":"787",
       "lines":[{"catalogItemId":"sku-big","quantity":5}],
       "groups":[{"groupNo":1,"offerId":"offer-1","tier":"SAME_DAY","lineNos":[1]}]}
      """
    Then the API answers 201
    And the response field "groups.0.deliveryCharge.amountMinor" is 2490
    And the response field "totalMinor" is 102490

  Scenario: The free-over threshold is judged per group, not on the whole quote
    Given a tax rate of 0 basis points everywhere from version 1
    When the API posts to "/price-quotes" the body:
      """
      {"cartId":"cart-1","destinationPostalPrefix":"787",
       "lines":[{"catalogItemId":"sku-edge","quantity":1},{"catalogItemId":"sku-2","quantity":1}],
       "groups":[{"groupNo":1,"offerId":"offer-1","tier":"NEXT_DAY","lineNos":[1]},
                 {"groupNo":2,"offerId":"offer-2","tier":"NEXT_DAY","lineNos":[2]}]}
      """
    Then the API answers 201
    And the response field "groups.0.deliveryCharge.amountMinor" is 1490
    And the response field "groups.1.deliveryCharge.amountMinor" is 1490
    And the response field "totalMinor" is 23279

  Scenario: A group's goods are the sum of all its lines
    Given a tax rate of 0 basis points everywhere from version 1
    When the API posts to "/price-quotes" the body:
      """
      {"cartId":"cart-1","destinationPostalPrefix":"787",
       "lines":[{"catalogItemId":"sku-edge","quantity":1},{"catalogItemId":"sku-2","quantity":1}],
       "groups":[{"groupNo":1,"offerId":"offer-1","tier":"NEXT_DAY","lineNos":[1,2]}]}
      """
    Then the API answers 201
    And the response field "groups.0.deliveryCharge.amountMinor" is 0
    And the response field "totalMinor" is 20299

  Scenario Outline: Tax is the rate of the band holding the destination prefix, edges included
    Given the API accepts a POST to "/tax-tables" with the body:
      """
      {"version":1,"bands":[
        {"postalPrefixFrom":"010","postalPrefixTo":"099","rateBasisPoints":1800},
        {"postalPrefixFrom":"100","postalPrefixTo":"199","rateBasisPoints":500}]}
      """
    When the API posts to "/price-quotes" the body:
      """
      {"cartId":"cart-1","destinationPostalPrefix":"<prefix>",
       "lines":[{"catalogItemId":"sku-big","quantity":1}],
       "groups":[{"groupNo":1,"offerId":"offer-1","tier":"NEXT_DAY","lineNos":[1]}]}
      """
    Then the API answers 201
    And the response field "taxMinor" is <tax>
    And the response field "totalMinor" is <total>

    Examples: both edges of both bands, then prefixes outside every band
      | prefix | tax  | total |
      | 010    | 3600 | 23600 |
      | 099    | 3600 | 23600 |
      | 100    | 1000 | 21000 |
      | 199    | 1000 | 21000 |
      | 009    | 0    | 20000 |
      | 200    | 0    | 20000 |

  Scenario Outline: Tax is rounded half up to a minor unit
    Given a tax rate of 500 basis points everywhere from version 1
    And the item "<item>" is priced at <price>
    When the API posts to "/price-quotes" the body:
      """
      {"cartId":"cart-1","destinationPostalPrefix":"787",
       "lines":[{"catalogItemId":"<item>","quantity":1}],
       "groups":[{"groupNo":1,"offerId":"offer-1","tier":"STANDARD","lineNos":[1]}]}
      """
    Then the API answers 201
    And the response field "taxMinor" is <tax>
    And the response field "totalMinor" is <total>

    Examples: 30.25 rounds down, 30.50 rounds up, 30.75 rounds up (goods plus a 590 delivery charge)
      | item   | price | tax | total |
      | round1 | 15    | 30  | 635   |
      | round2 | 20    | 31  | 641   |
      | round3 | 25    | 31  | 646   |

  Scenario: A line is priced at the sale price while a sale is open and at the list price before it opens
    Given the API accepts a POST to "/item-prices/sku-1/sales" the body:
      """
      {"price":{"amountMinor":800,"currency":"BRL"},"from":"2026-10-24T08:00:00Z","to":"2026-10-25T08:00:00Z"}
      """
    And the API accepts a POST to "/item-prices/sku-2/sales" the body:
      """
      {"price":{"amountMinor":200,"currency":"BRL"},"from":"2026-10-26T00:00:00Z","to":"2026-10-27T00:00:00Z"}
      """
    And a tax rate of 0 basis points everywhere from version 1
    When the API posts to "/price-quotes" the body:
      """
      {"cartId":"cart-1","destinationPostalPrefix":"787",
       "lines":[{"catalogItemId":"sku-1","quantity":2},{"catalogItemId":"sku-2","quantity":1}],
       "groups":[{"groupNo":1,"offerId":"offer-1","tier":"STANDARD","lineNos":[1,2]}]}
      """
    Then the API answers 201
    And the response field "lines.0.unitPrice.amountMinor" is 800
    And the response field "lines.0.lineTotal.amountMinor" is 1600
    And the response field "lines.1.unitPrice.amountMinor" is 300

  Scenario: A quote keeps the schedule and tax table versions it was issued with
    Given a tax rate of 1000 basis points everywhere from version 1
    And the API posts to "/price-quotes" the body:
      """
      {"cartId":"cart-1","destinationPostalPrefix":"787",
       "lines":[{"catalogItemId":"sku-2","quantity":1}],
       "groups":[{"groupNo":1,"offerId":"offer-1","tier":"STANDARD","lineNos":[1]}]}
      """
    And I remember the response field "quoteId" as "first"
    And the API accepts a POST to "/delivery-charge-schedules" with the body:
      """
      {"version":2,"tiers":[
        {"tier":"SAME_DAY","charge":{"amountMinor":9000,"currency":"BRL"}},
        {"tier":"NEXT_DAY","charge":{"amountMinor":9000,"currency":"BRL"}},
        {"tier":"TWO_DAY","charge":{"amountMinor":9000,"currency":"BRL"}},
        {"tier":"STANDARD","charge":{"amountMinor":9000,"currency":"BRL"}}]}
      """
    And a tax rate of 2000 basis points everywhere from version 2
    When the API gets the quote remembered as "first"
    Then the response field "versions.chargeSchedule" is 1
    And the response field "versions.taxTable" is 1
    And the response field "versions.price:sku-2" is 1
    And the response field "groups.0.deliveryCharge.amountMinor" is 590
    And the response field "totalMinor" is 979
    When the API posts to "/price-quotes" the body:
      """
      {"cartId":"cart-2","destinationPostalPrefix":"787",
       "lines":[{"catalogItemId":"sku-2","quantity":1}],
       "groups":[{"groupNo":1,"offerId":"offer-1","tier":"STANDARD","lineNos":[1]}]}
      """
    Then the response field "versions.chargeSchedule" is 2
    And the response field "versions.taxTable" is 2
    And the response field "totalMinor" is 11160

  Scenario Outline: A line holds 1 to 1000 units
    Given a tax rate of 0 basis points everywhere from version 1
    When the API posts to "/price-quotes" the body:
      """
      {"cartId":"cart-1","destinationPostalPrefix":"787",
       "lines":[{"catalogItemId":"sku-2","quantity":<quantity>}],
       "groups":[{"groupNo":1,"offerId":"offer-1","tier":"STANDARD","lineNos":[1]}]}
      """
    Then the API answers <status>

    Examples: the smallest and largest quantity, then one outside each end
      | quantity | status |
      | 1        | 201    |
      | 1000     | 201    |
      | 0        | 422    |
      | 1001     | 422    |

  Scenario: An item cannot be on two lines of one quote
    Given a tax rate of 0 basis points everywhere from version 1
    When the API posts to "/price-quotes" the body:
      """
      {"cartId":"cart-1","destinationPostalPrefix":"787",
       "lines":[{"catalogItemId":"sku-2","quantity":1},{"catalogItemId":"sku-2","quantity":2}],
       "groups":[{"groupNo":1,"offerId":"offer-1","tier":"STANDARD","lineNos":[1,2]}]}
      """
    Then the API answers 422 with problem "validation-failed"

  Scenario Outline: Every line is in exactly one group of a valid, numbered tier
    Given a tax rate of 0 basis points everywhere from version 1
    When the API posts to "/price-quotes" the body:
      """
      {"cartId":"cart-1","destinationPostalPrefix":"787",
       "lines":[{"catalogItemId":"sku-1","quantity":1},{"catalogItemId":"sku-2","quantity":1}],
       "groups":<groups>}
      """
    Then the API answers 422 with problem "validation-failed"
    And the store holds 0 quotes

    Examples: no groups, a line in no group, a line in two groups, a line that does not exist, a group without lines, a repeated group number, group number 0, a tier that does not exist
      | groups                                                                                                                                                   |
      | []                                                                                                                                                       |
      | [{"groupNo":1,"offerId":"o1","tier":"STANDARD","lineNos":[1]}]                                                                                           |
      | [{"groupNo":1,"offerId":"o1","tier":"STANDARD","lineNos":[1,2]},{"groupNo":2,"offerId":"o2","tier":"STANDARD","lineNos":[2]}]                            |
      | [{"groupNo":1,"offerId":"o1","tier":"STANDARD","lineNos":[1,2,3]}]                                                                                       |
      | [{"groupNo":1,"offerId":"o1","tier":"STANDARD","lineNos":[1,2]},{"groupNo":2,"offerId":"o2","tier":"STANDARD","lineNos":[]}]                             |
      | [{"groupNo":1,"offerId":"o1","tier":"STANDARD","lineNos":[1]},{"groupNo":1,"offerId":"o2","tier":"STANDARD","lineNos":[2]}]                              |
      | [{"groupNo":0,"offerId":"o1","tier":"STANDARD","lineNos":[1,2]}]                                                                                         |
      | [{"groupNo":1,"offerId":"o1","tier":"OVERNIGHT","lineNos":[1,2]}]                                                                                        |

  Scenario Outline: A destination postal prefix is exactly 3 upper-case letters or digits
    Given a tax rate of 0 basis points everywhere from version 1
    When the API posts to "/price-quotes" the body:
      """
      {"cartId":"cart-1","destinationPostalPrefix":"<prefix>",
       "lines":[{"catalogItemId":"sku-2","quantity":1}],
       "groups":[{"groupNo":1,"offerId":"offer-1","tier":"STANDARD","lineNos":[1]}]}
      """
    Then the API answers 422 with problem "validation-failed"

    Examples: lower case, too short, too long, empty
      | prefix |
      | sao    |
      | 78     |
      | 7870   |
      |        |

  Scenario: A quote holds at most 100 lines
    Given a tax rate of 0 basis points everywhere from version 1
    And 101 items named "bulk" numbered from 1 are priced at 100
    When the API issues a quote for the items "bulk" numbered 1 to 100 in groups of 2 to postal prefix "787"
    Then the API answers 201
    When the API issues a quote for the items "bulk" numbered 1 to 101 in groups of 3 to postal prefix "787"
    Then the API answers 422 with problem "validation-failed"

  Scenario: A quote holds at most 50 groups
    Given a tax rate of 0 basis points everywhere from version 1
    And 51 items named "bulk" numbered from 1 are priced at 100
    When the API issues a quote for the items "bulk" numbered 1 to 50 in groups of 1 to postal prefix "787"
    Then the API answers 201
    When the API issues a quote for the items "bulk" numbered 1 to 51 in groups of 1 to postal prefix "787"
    Then the API answers 422 with problem "validation-failed"

  Scenario: An item without an active price cannot be quoted and nothing is issued
    Given a tax rate of 0 basis points everywhere from version 1
    And the catalogue item "sku-unpriced" is known
    And the API posts to "/item-prices/sku-2/withdrawals" with no body
    When the API posts to "/price-quotes" the body:
      """
      {"cartId":"cart-1","destinationPostalPrefix":"787",
       "lines":[{"catalogItemId":"sku-unpriced","quantity":1}],
       "groups":[{"groupNo":1,"offerId":"offer-1","tier":"STANDARD","lineNos":[1]}]}
      """
    Then the API answers 422 with problem "item-not-priced"
    When the API posts to "/price-quotes" the body:
      """
      {"cartId":"cart-1","destinationPostalPrefix":"787",
       "lines":[{"catalogItemId":"sku-1","quantity":1},{"catalogItemId":"sku-2","quantity":1}],
       "groups":[{"groupNo":1,"offerId":"offer-1","tier":"STANDARD","lineNos":[1,2]}]}
      """
    Then the API answers 422 with problem "item-not-priced"
    And the store holds 0 quotes
    And 0 "PriceQuoteIssued" events were published

  Scenario: An issued quote stays readable and unchanged after its item is withdrawn
    Given a tax rate of 0 basis points everywhere from version 1
    And the API posts to "/price-quotes" the body:
      """
      {"cartId":"cart-1","destinationPostalPrefix":"787",
       "lines":[{"catalogItemId":"sku-2","quantity":2}],
       "groups":[{"groupNo":1,"offerId":"offer-1","tier":"STANDARD","lineNos":[1]}]}
      """
    And I remember the response field "quoteId" as "kept"
    When the API posts to "/item-prices/sku-2/withdrawals" with no body
    And the API gets the quote remembered as "kept"
    Then the API answers 200
    And the response field "state" is "ISSUED"
    And the response field "totalMinor" is 1190
