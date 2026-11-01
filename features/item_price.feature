Feature: Merchandising sets list prices and sale windows per catalogue item
  An ItemPrice exists only for a catalogue item the local CatalogItemView knows.
  The effective price is the live sale price, else the list price, and every
  change of it is published as ItemPriceChanged.

  Scenario: A known item gets a list price
    Given the catalogue item "sku-1" is known
    When the API posts to "/item-prices" the body:
      """
      {"catalogItemId":"sku-1","listPrice":{"amountMinor":1000,"currency":"BRL"}}
      """
    Then the API answers 201
    And the response field "state" is "ACTIVE"
    And the response field "effectivePrice.amountMinor" is 1000
    And 1 "ItemPriceChanged" events were published

  Scenario: An unknown item cannot be priced
    When the API posts to "/item-prices" the body:
      """
      {"catalogItemId":"ghost","listPrice":{"amountMinor":1000,"currency":"BRL"}}
      """
    Then the API answers 422 with problem "unknown-catalog-item"

  Scenario: A price in another currency is refused
    Given the catalogue item "sku-1" is known
    When the API posts to "/item-prices" the body:
      """
      {"catalogItemId":"sku-1","listPrice":{"amountMinor":1000,"currency":"USD"}}
      """
    Then the API answers 422 with problem "currency-mismatch"

  Scenario: Changing the list price publishes the new effective price
    Given the item "sku-1" is priced at 1000
    When the API puts to "/item-prices/sku-1/list-price" the body:
      """
      {"listPrice":{"amountMinor":1200,"currency":"BRL"}}
      """
    Then the API answers 200
    And the response field "effectivePrice.amountMinor" is 1200
    And 2 "ItemPriceChanged" events were published

  Scenario: Withdrawing a price is final
    Given the item "sku-1" is priced at 1000
    When the API posts to "/item-prices/sku-1/withdrawals" with no body
    Then the API answers 200
    And the response field "state" is "WITHDRAWN"
    And 1 "ItemPriceWithdrawn" events were published
    When the API posts to "/item-prices/sku-1/withdrawals" with no body
    Then the API answers 409 with problem "illegal-transition"

  Scenario: Discontinuing the item withdraws its price
    Given the item "sku-1" is priced at 1000
    When offer-availability discontinues the item "sku-1"
    And the API gets "/item-prices/sku-1"
    Then the response field "state" is "WITHDRAWN"
    And 1 "ItemPriceWithdrawn" events were published
