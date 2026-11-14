# quote_lifecycle_rules.feature — wave 3: the PriceQuote lifecycle and the policy
# tables it needs (use-cases IssuePriceQuote / ExpireQuote, aggregate-design-canvas
# PriceQuote, domain-events PriceQuoteIssued, ADR 0002 A11-A12 and A14). The clock
# starts at 2026-10-24T09:00:00Z and the QuoteTtl is 15 minutes.
Feature: A price quote is binding until it expires and expiry is by time
  A quote reads EXPIRED from its expiresAt instant on; the clock-driven policy
  stores the state and publishes nothing, by decision. A quote needs a current
  delivery charge schedule and tax table.

  Background:
    Given the item "sku-2" is priced at 300

  Scenario: A quote with no current delivery charge schedule is refused
    Given a tax rate of 1000 basis points everywhere from version 1
    When the API posts to "/price-quotes" the body:
      """
      {"cartId":"cart-1","destinationPostalPrefix":"787",
       "lines":[{"catalogItemId":"sku-2","quantity":1}],
       "groups":[{"groupNo":1,"offerId":"offer-1","tier":"STANDARD","lineNos":[1]}]}
      """
    Then the API answers 422 with problem "no-charge-schedule"
    And the store holds 0 quotes

  Scenario: A quote with no current tax table is refused
    Given every delivery tier charges 500 from version 1
    When the API posts to "/price-quotes" the body:
      """
      {"cartId":"cart-1","destinationPostalPrefix":"787",
       "lines":[{"catalogItemId":"sku-2","quantity":1}],
       "groups":[{"groupNo":1,"offerId":"offer-1","tier":"STANDARD","lineNos":[1]}]}
      """
    Then the API answers 422 with problem "no-tax-table"
    And the store holds 0 quotes

  Scenario: A quote expires fifteen minutes after issue, at that very instant
    Given every delivery tier charges 500 from version 1
    And a tax rate of 1000 basis points everywhere from version 1
    When the API posts to "/price-quotes" the body:
      """
      {"cartId":"cart-1","destinationPostalPrefix":"787",
       "lines":[{"catalogItemId":"sku-2","quantity":1}],
       "groups":[{"groupNo":1,"offerId":"offer-1","tier":"STANDARD","lineNos":[1]}]}
      """
    Then the API answers 201
    And the response field "issuedAt" is "2026-10-24T09:00:00Z"
    And the response field "expiresAt" is "2026-10-24T09:15:00Z"
    When 899 seconds pass
    And the API gets "/price-quotes/quote-1"
    Then the response field "state" is "ISSUED"
    When 1 seconds pass
    And the API gets "/price-quotes/quote-1"
    Then the response field "state" is "EXPIRED"
    And the response field "totalMinor" is 880

  Scenario: The expiry policy stores EXPIRED only for quotes past their time and publishes nothing
    Given every delivery tier charges 500 from version 1
    And a tax rate of 1000 basis points everywhere from version 1
    And the API accepts a POST to "/price-quotes" with the body:
      """
      {"cartId":"cart-1","destinationPostalPrefix":"787",
       "lines":[{"catalogItemId":"sku-2","quantity":1}],
       "groups":[{"groupNo":1,"offerId":"offer-1","tier":"STANDARD","lineNos":[1]}]}
      """
    And 10 minutes pass
    And the API accepts a POST to "/price-quotes" with the body:
      """
      {"cartId":"cart-2","destinationPostalPrefix":"787",
       "lines":[{"catalogItemId":"sku-2","quantity":1}],
       "groups":[{"groupNo":1,"offerId":"offer-1","tier":"STANDARD","lineNos":[1]}]}
      """
    When 5 minutes pass
    And the policies run
    Then the stored quote "quote-1" is EXPIRED
    And the stored quote "quote-2" is ISSUED
    And 0 "PriceQuoteExpired" events were published
    And 5 events were published in total
    When the policies run
    Then the stored quote "quote-1" is EXPIRED
    And the stored quote "quote-2" is ISSUED
    And 5 events were published in total

  Scenario: The expiry policy leaves a quote one second short of its time ISSUED
    Given every delivery tier charges 500 from version 1
    And a tax rate of 1000 basis points everywhere from version 1
    And the API accepts a POST to "/price-quotes" with the body:
      """
      {"cartId":"cart-1","destinationPostalPrefix":"787",
       "lines":[{"catalogItemId":"sku-2","quantity":1}],
       "groups":[{"groupNo":1,"offerId":"offer-1","tier":"STANDARD","lineNos":[1]}]}
      """
    When 899 seconds pass
    And the policies run
    Then the stored quote "quote-1" is ISSUED
    When 1 seconds pass
    And the policies run
    Then the stored quote "quote-1" is EXPIRED

  Scenario: A retried open of the same checkout session returns the same quote
    Given every delivery tier charges 500 from version 1
    And a tax rate of 1000 basis points everywhere from version 1
    When the API posts to "/price-quotes" with Idempotency-Key "session-9" the body:
      """
      {"cartId":"cart-9","destinationPostalPrefix":"787",
       "lines":[{"catalogItemId":"sku-2","quantity":1}],
       "groups":[{"groupNo":1,"offerId":"offer-1","tier":"STANDARD","lineNos":[1]}]}
      """
    Then the API answers 201
    And the response field "quoteId" is "quote-1"
    When the API posts to "/price-quotes" with Idempotency-Key "session-9" the body:
      """
      {"cartId":"cart-9","destinationPostalPrefix":"787",
       "lines":[{"catalogItemId":"sku-2","quantity":1}],
       "groups":[{"groupNo":1,"offerId":"offer-1","tier":"STANDARD","lineNos":[1]}]}
      """
    Then the API answers 201
    And the response is marked replayed
    And the response field "quoteId" is "quote-1"
    And the store holds 1 quotes
    And 1 "PriceQuoteIssued" events were published

  Scenario: PriceQuoteIssued names the quote, the cart, the binding total and the expiry
    Given every delivery tier charges 500 from version 1
    And a tax rate of 1000 basis points everywhere from version 1
    When the API posts to "/price-quotes" the body:
      """
      {"cartId":"cart-77","destinationPostalPrefix":"787",
       "lines":[{"catalogItemId":"sku-2","quantity":1}],
       "groups":[{"groupNo":1,"offerId":"offer-1","tier":"STANDARD","lineNos":[1]}]}
      """
    Then the latest "PriceQuoteIssued" event totals 880 for the cart "cart-77" and expires at "2026-10-24T09:15:00Z"
    And the latest "PriceQuoteIssued" event carries version 1 and is keyed by "quote-1"

  Scenario: A quote that does not exist is not-found
    When the API gets "/price-quotes/quote-404"
    Then the API answers 404 with problem "not-found"
