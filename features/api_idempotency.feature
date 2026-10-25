Feature: Writes are idempotent on the Idempotency-Key and errors are problem documents
  The first response below 500 of a (route, key) is stored; the same key with the
  same body replays it and nothing runs again. The same key with another body is
  refused as a reuse.

  Background:
    Given the catalogue item "sku-1" is known

  Scenario: A write without an Idempotency-Key is refused
    When the API posts to "/item-prices" without an Idempotency-Key
    Then the API answers 400 with problem "idempotency-key-required"

  Scenario: A replayed request returns the stored answer and publishes once
    When the API posts to "/item-prices" with Idempotency-Key "key-1" the body:
      """
      {"catalogItemId":"sku-1","listPrice":{"amountMinor":1000,"currency":"BRL"}}
      """
    Then the API answers 201
    When the API posts to "/item-prices" with Idempotency-Key "key-1" the body:
      """
      {"catalogItemId":"sku-1","listPrice":{"amountMinor":1000,"currency":"BRL"}}
      """
    Then the API answers 201
    And the response is marked replayed
    And 1 "ItemPriceChanged" events were published

  Scenario: The same key with another body is refused as a reuse
    Given the API posts to "/item-prices" with Idempotency-Key "key-2" the body:
      """
      {"catalogItemId":"sku-1","listPrice":{"amountMinor":1000,"currency":"BRL"}}
      """
    When the API posts to "/item-prices" with Idempotency-Key "key-2" the body:
      """
      {"catalogItemId":"sku-1","listPrice":{"amountMinor":5,"currency":"BRL"}}
      """
    Then the API answers 422 with problem "idempotency-key-reused"

  Scenario: A malformed body is a problem document
    When the API posts to "/item-prices" the body:
      """
      {"unknown":true}
      """
    Then the API answers 400 with problem "malformed-request"

  Scenario: A missing resource is a not-found problem
    When the API gets "/item-prices/ghost"
    Then the API answers 404 with problem "not-found"
