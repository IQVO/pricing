# item_price_rules.feature — wave 3: ItemPrice rules taken from the documentation
# (aggregate-design-canvas, use-cases SetListPrice / WithdrawPrice, ADR 0002 A2-A6,
# operations troubleshooting), each pinned by its outcome.
Feature: An ItemPrice keeps its invariants through its two-state lifecycle
  ACTIVE -> WITHDRAWN is final. Every amount is zero or more, in the configured
  currency and within the documented ceiling; a new list price may not sink to
  an existing sale price; the optional version guard turns a stale write into a
  concurrent-update.

  Scenario: Pricing an item that is already priced is a concurrent update
    Given the item "sku-1" is priced at 1000
    When the API posts to "/item-prices" the body:
      """
      {"catalogItemId":"sku-1","listPrice":{"amountMinor":1100,"currency":"BRL"}}
      """
    Then the API answers 409 with problem "concurrent-update"
    When the API gets "/item-prices/sku-1"
    Then the response field "listPrice.amountMinor" is 1000
    And the response field "version" is 1
    And 1 "ItemPriceChanged" events were published

  Scenario: A list price of zero is accepted
    Given the catalogue item "sku-free" is known
    When the API posts to "/item-prices" the body:
      """
      {"catalogItemId":"sku-free","listPrice":{"amountMinor":0,"currency":"BRL"}}
      """
    Then the API answers 201
    And the response field "effectivePrice.amountMinor" is 0

  Scenario: A negative list price is refused and nothing is published
    Given the catalogue item "sku-1" is known
    When the API posts to "/item-prices" the body:
      """
      {"catalogItemId":"sku-1","listPrice":{"amountMinor":-1,"currency":"BRL"}}
      """
    Then the API answers 422 with problem "negative-amount"
    And 0 "ItemPriceChanged" events were published
    And the store holds 0 item prices

  Scenario: The largest amount is accepted and one minor unit more is refused
    Given the catalogue item "sku-1" is known
    And the catalogue item "sku-2" is known
    When the API posts to "/item-prices" the body:
      """
      {"catalogItemId":"sku-1","listPrice":{"amountMinor":1000000000,"currency":"BRL"}}
      """
    Then the API answers 201
    When the API posts to "/item-prices" the body:
      """
      {"catalogItemId":"sku-2","listPrice":{"amountMinor":1000000001,"currency":"BRL"}}
      """
    Then the API answers 422 with problem "validation-failed"

  Scenario Outline: A catalogue item id holds 1 to 64 characters
    When the item whose id has <chars> characters is priced at 1000
    Then the API answers <status>

    Examples:
      | chars | status |
      | 64    | 201    |
      | 65    | 422    |

  Scenario: A catalogue item id with a character outside the id alphabet is refused
    When the API posts to "/item-prices" the body:
      """
      {"catalogItemId":"has space","listPrice":{"amountMinor":1000,"currency":"BRL"}}
      """
    Then the API answers 422 with problem "validation-failed"

  Scenario: A withdrawn price accepts no further command
    Given the item "sku-1" is priced at 1000
    And the API accepts a POST to "/item-prices/sku-1/sales" the body:
      """
      {"price":{"amountMinor":800,"currency":"BRL"},"from":"2026-11-01T00:00:00Z","to":"2026-11-02T00:00:00Z"}
      """
    When the API posts to "/item-prices/sku-1/withdrawals" with no body
    Then the API answers 200
    When the API puts to "/item-prices/sku-1/list-price" the body:
      """
      {"listPrice":{"amountMinor":1200,"currency":"BRL"}}
      """
    Then the API answers 409 with problem "illegal-transition"
    When the API posts to "/item-prices/sku-1/sales" the body:
      """
      {"price":{"amountMinor":700,"currency":"BRL"},"from":"2026-12-01T00:00:00Z","to":"2026-12-02T00:00:00Z"}
      """
    Then the API answers 409 with problem "illegal-transition"
    When the API posts to "/item-prices/sku-1/sales/sale-1/cancellations" with no body
    Then the API answers 409 with problem "illegal-transition"
    When the API gets "/item-prices/sku-1"
    Then the response field "state" is "WITHDRAWN"
    And the response field "listPrice.amountMinor" is 1000
    And 1 "ItemPriceChanged" events were published
    And 1 "ItemPriceWithdrawn" events were published

  Scenario: Withdrawing a price a second time publishes no second withdrawal
    Given the item "sku-1" is priced at 1000
    When the API posts to "/item-prices/sku-1/withdrawals" with no body
    And the API posts to "/item-prices/sku-1/withdrawals" with no body
    And the API gets "/item-prices/sku-1"
    Then the response field "state" is "WITHDRAWN"
    And 1 "ItemPriceWithdrawn" events were published

  Scenario: Commands on an item that was never priced are not-found
    Given the catalogue item "sku-1" is known
    When the API puts to "/item-prices/sku-1/list-price" the body:
      """
      {"listPrice":{"amountMinor":1200,"currency":"BRL"}}
      """
    Then the API answers 404 with problem "not-found"
    When the API posts to "/item-prices/sku-1/sales" the body:
      """
      {"price":{"amountMinor":700,"currency":"BRL"},"from":"2026-11-01T00:00:00Z","to":"2026-11-02T00:00:00Z"}
      """
    Then the API answers 404 with problem "not-found"
    When the API posts to "/item-prices/sku-1/withdrawals" with no body
    Then the API answers 404 with problem "not-found"
    When the API posts to "/item-prices/sku-1/sales/sale-1/cancellations" with no body
    Then the API answers 404 with problem "not-found"

  Scenario: A list price write carrying the current version is applied and a stale one is a concurrent update
    Given the item "sku-1" is priced at 1000
    When the API puts to "/item-prices/sku-1/list-price" the body:
      """
      {"listPrice":{"amountMinor":1200,"currency":"BRL"},"version":1}
      """
    Then the API answers 200
    And the response field "version" is 2
    When the API puts to "/item-prices/sku-1/list-price" the body:
      """
      {"listPrice":{"amountMinor":1300,"currency":"BRL"},"version":1}
      """
    Then the API answers 409 with problem "concurrent-update"
    When the API gets "/item-prices/sku-1"
    Then the response field "listPrice.amountMinor" is 1200

  Scenario: Setting the list price to the amount it already has publishes nothing
    Given the item "sku-1" is priced at 1000
    When the API puts to "/item-prices/sku-1/list-price" the body:
      """
      {"listPrice":{"amountMinor":1000,"currency":"BRL"}}
      """
    Then the API answers 200
    And 1 "ItemPriceChanged" events were published

  Scenario: An active sale still wins over a new list price
    Given the item "sku-1" is priced at 1000
    And the API accepts a POST to "/item-prices/sku-1/sales" the body:
      """
      {"price":{"amountMinor":800,"currency":"BRL"},"from":"2026-10-24T08:00:00Z","to":"2026-10-25T08:00:00Z"}
      """
    When the API puts to "/item-prices/sku-1/list-price" the body:
      """
      {"listPrice":{"amountMinor":1200,"currency":"BRL"}}
      """
    Then the API answers 200
    And the response field "listPrice.amountMinor" is 1200
    And the response field "effectivePrice.amountMinor" is 800
    And 3 "ItemPriceChanged" events were published
    And the latest "ItemPriceChanged" event has list price 1200 and effective price 800
    And the latest "ItemPriceChanged" event has its sale ending at "2026-10-25T08:00:00Z"

  Scenario: A new list price at an existing sale price is refused and one minor unit above is accepted
    Given the item "sku-1" is priced at 1000
    And the API accepts a POST to "/item-prices/sku-1/sales" the body:
      """
      {"price":{"amountMinor":800,"currency":"BRL"},"from":"2026-10-24T08:00:00Z","to":"2026-10-25T08:00:00Z"}
      """
    When the API puts to "/item-prices/sku-1/list-price" the body:
      """
      {"listPrice":{"amountMinor":800,"currency":"BRL"}}
      """
    Then the API answers 422 with problem "sale-not-below-list"
    When the API puts to "/item-prices/sku-1/list-price" the body:
      """
      {"listPrice":{"amountMinor":801,"currency":"BRL"}}
      """
    Then the API answers 200
    And the response field "effectivePrice.amountMinor" is 800

  Scenario: A new list price is also held above a sale that has not opened yet
    Given the item "sku-1" is priced at 1000
    And the API accepts a POST to "/item-prices/sku-1/sales" the body:
      """
      {"price":{"amountMinor":800,"currency":"BRL"},"from":"2026-11-01T00:00:00Z","to":"2026-11-02T00:00:00Z"}
      """
    When the API puts to "/item-prices/sku-1/list-price" the body:
      """
      {"listPrice":{"amountMinor":700,"currency":"BRL"}}
      """
    Then the API answers 422 with problem "sale-not-below-list"

  Scenario: Every command refuses an amount outside the configured currency or below zero
    Given the item "sku-1" is priced at 1000
    When the API puts to "/item-prices/sku-1/list-price" the body:
      """
      {"listPrice":{"amountMinor":1200,"currency":"USD"}}
      """
    Then the API answers 422 with problem "currency-mismatch"
    When the API puts to "/item-prices/sku-1/list-price" the body:
      """
      {"listPrice":{"amountMinor":-5,"currency":"BRL"}}
      """
    Then the API answers 422 with problem "negative-amount"
    When the API posts to "/item-prices/sku-1/sales" the body:
      """
      {"price":{"amountMinor":800,"currency":"USD"},"from":"2026-11-01T00:00:00Z","to":"2026-11-02T00:00:00Z"}
      """
    Then the API answers 422 with problem "currency-mismatch"
    When the API posts to "/item-prices/sku-1/sales" the body:
      """
      {"price":{"amountMinor":-1,"currency":"BRL"},"from":"2026-11-01T00:00:00Z","to":"2026-11-02T00:00:00Z"}
      """
    Then the API answers 422 with problem "negative-amount"
    And 1 "ItemPriceChanged" events were published
