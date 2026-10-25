Feature: Checkout obtains a binding, expiring price quote
  The quote prices exactly what the checkout session froze: lines at the
  effective price now, groups with the delivery charge of their tier, tax of the
  destination band, and the versions it used. It is immutable and expires.

  Background:
    Given the item "sku-1" is priced at 1000
    And the item "sku-2" is priced at 300
    And every delivery tier charges 500 from version 1
    And a tax rate of 1000 basis points everywhere from version 1

  Scenario: A quote totals goods, delivery and tax
    When the API posts to "/price-quotes" the body:
      """
      {"cartId":"cart-1","destinationPostalPrefix":"787",
       "lines":[{"catalogItemId":"sku-1","quantity":2},{"catalogItemId":"sku-2","quantity":1}],
       "groups":[{"groupNo":1,"offerId":"offer-1","tier":"STANDARD","lineNos":[1,2]}]}
      """
    Then the API answers 201
    And the response field "state" is "ISSUED"
    And the response field "lines.0.lineTotal.amountMinor" is 2000
    And the response field "groups.0.deliveryCharge.amountMinor" is 500
    And the response field "taxMinor" is 280
    And the response field "totalMinor" is 3080
    And the response field "versions.chargeSchedule" is 1
    And 1 "PriceQuoteIssued" events were published

  Scenario: A quote cannot include an item without an active price
    When the API posts to "/price-quotes" the body:
      """
      {"cartId":"cart-1","destinationPostalPrefix":"787",
       "lines":[{"catalogItemId":"ghost","quantity":1}],
       "groups":[{"groupNo":1,"offerId":"offer-1","tier":"STANDARD","lineNos":[1]}]}
      """
    Then the API answers 422 with problem "item-not-priced"

  Scenario: A quote with no lines is refused
    When the API posts to "/price-quotes" the body:
      """
      {"cartId":"cart-1","destinationPostalPrefix":"787","lines":[],"groups":[]}
      """
    Then the API answers 422 with problem "empty-quote"

  Scenario: A quote is immutable when the price changes afterwards
    Given the API posts to "/price-quotes" the body:
      """
      {"cartId":"cart-1","destinationPostalPrefix":"787",
       "lines":[{"catalogItemId":"sku-2","quantity":1}],
       "groups":[{"groupNo":1,"offerId":"offer-1","tier":"STANDARD","lineNos":[1]}]}
      """
    And I remember the response field "quoteId" as "quote"
    When the API posts to "/item-prices/sku-2:set-list-price" the body:
      """
      {"listPrice":{"amountMinor":999,"currency":"BRL"}}
      """
    And the API gets "/price-quotes/quote-1"
    Then the response field "lines.0.unitPrice.amountMinor" is 300

  Scenario: A quote expires after its time to live
    Given the API posts to "/price-quotes" the body:
      """
      {"cartId":"cart-1","destinationPostalPrefix":"787",
       "lines":[{"catalogItemId":"sku-2","quantity":1}],
       "groups":[{"groupNo":1,"offerId":"offer-1","tier":"STANDARD","lineNos":[1]}]}
      """
    And 16 minutes pass
    When the API gets "/price-quotes/quote-1"
    Then the response field "state" is "EXPIRED"
