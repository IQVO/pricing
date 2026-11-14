# api_contract_rules.feature — wave 3: the REST contract rules of the operations
# documentation that no scenario pinned: idempotency record lifetime and
# refusals, paging bounds, and the shapes of a malformed request
# (ADR 0002 A13 and A16, ADR 0008, operations troubleshooting "REST problem types").
Feature: The REST contract stores first answers, bounds its pages and rejects malformed requests
  Background:
    Given the catalogue item "sku-1" is known

  Scenario: The first answer below 500 is stored, a refusal included, and replayed under its key
    When the API posts to "/item-prices" with Idempotency-Key "late-key" the body:
      """
      {"catalogItemId":"sku-late","listPrice":{"amountMinor":1000,"currency":"BRL"}}
      """
    Then the API answers 422 with problem "unknown-catalog-item"
    Given the catalogue item "sku-late" is known
    When the API posts to "/item-prices" with Idempotency-Key "late-key" the body:
      """
      {"catalogItemId":"sku-late","listPrice":{"amountMinor":1000,"currency":"BRL"}}
      """
    Then the API answers 422 with problem "unknown-catalog-item"
    And the response is marked replayed
    When the API posts to "/item-prices" with Idempotency-Key "fresh-key" the body:
      """
      {"catalogItemId":"sku-late","listPrice":{"amountMinor":1000,"currency":"BRL"}}
      """
    Then the API answers 201
    And the response is not marked replayed

  Scenario: An Idempotency-Key of exactly 128 characters is accepted
    When the API posts to "/item-prices" with an Idempotency-Key of 128 characters the body:
      """
      {"catalogItemId":"sku-1","listPrice":{"amountMinor":1000,"currency":"BRL"}}
      """
    Then the API answers 201

  Scenario: A key is remembered for 24 hours and is free afterwards
    When the API posts to "/item-prices" with Idempotency-Key "ttl-key" the body:
      """
      {"catalogItemId":"sku-1","listPrice":{"amountMinor":1000,"currency":"BRL"}}
      """
    Then the API answers 201
    When 23 hours pass
    And 59 minutes pass
    And the API posts to "/item-prices" with Idempotency-Key "ttl-key" the body:
      """
      {"catalogItemId":"sku-1","listPrice":{"amountMinor":1000,"currency":"BRL"}}
      """
    Then the API answers 201
    And the response is marked replayed
    When 2 minutes pass
    And the API posts to "/item-prices" with Idempotency-Key "ttl-key" the body:
      """
      {"catalogItemId":"sku-1","listPrice":{"amountMinor":1000,"currency":"BRL"}}
      """
    Then the API answers 409 with problem "concurrent-update"
    And the response is not marked replayed

  Scenario Outline: A page holds 1 to 500 items
    When the API gets "/item-prices?limit=<limit>"
    Then the API answers 200

    Examples: the smallest and the largest page
      | limit |
      | 1     |
      | 500   |

  Scenario Outline: A limit outside 1 to 500 or not a number is refused
    When the API gets "/item-prices?limit=<limit>"
    Then the API answers 400 with problem "invalid-limit"

    Examples: one above the largest page, negative, not a number, empty
      | limit |
      | 501   |
      | -1    |
      | abc   |
      |       |

  Scenario: A page that exactly fills its limit has no next cursor
    Given 3 items named "page" numbered from 1 are priced at 100
    When the API gets "/item-prices?limit=3"
    Then the response list "items" has 3 items
    And the response has no field "nextCursor"
    When the API gets "/item-prices?limit=2"
    Then the response list "items" has 2 items
    And I remember the response field "nextCursor" as "after-two"
    When the API gets "/item-prices" with the remembered cursor "after-two"
    Then the response list "items" has 1 items
    And the response has no field "nextCursor"

  Scenario: Without a limit a page holds 100 items and the cursor leads to the rest
    Given 101 items named "bulk" numbered from 1 are priced at 100
    When the API gets "/item-prices"
    Then the response list "items" has 100 items
    And I remember the response field "nextCursor" as "page2"
    When the API gets "/item-prices" with the remembered cursor "page2"
    Then the response list "items" has 1 items
    And the response has no field "nextCursor"

  Scenario: A retired paging parameter fails loudly
    When the API gets "/item-prices?after=sku-1"
    Then the API answers 400 with problem "unknown-query-parameter"

  Scenario: A NUL character in a body, a query or a path is a client error
    When the API posts to "/item-prices" the body:
      """
      {"catalogItemId":"sku\u00001","listPrice":{"amountMinor":1000,"currency":"BRL"}}
      """
    Then the API answers 400 with problem "malformed-request"
    When the API gets "/item-prices?cursor=%00"
    Then the API answers 400 with problem "malformed-request"
    When the API gets "/item-prices/%00"
    Then the API answers 404 with problem "resource-not-found"

  Scenario: A body of 65536 bytes is read and one byte more is refused
    When the API posts to "/item-prices" a body of 65536 bytes
    Then the API answers 201
    When the API posts to "/item-prices" a body of 65537 bytes
    Then the API answers 400 with problem "malformed-request"

  Scenario: A body that is empty, has trailing data or a wrong type is malformed
    When the API posts to "/item-prices" with no body
    Then the API answers 400 with problem "malformed-request"
    When the API posts to "/item-prices" the body:
      """
      {"catalogItemId":"sku-1","listPrice":{"amountMinor":1000,"currency":"BRL"}}{"again":true}
      """
    Then the API answers 400 with problem "malformed-request"
    When the API posts to "/item-prices" the body:
      """
      {"catalogItemId":"sku-1","listPrice":{"amountMinor":"1000","currency":"BRL"}}
      """
    Then the API answers 400 with problem "malformed-request"
    And the store holds 0 item prices

  Scenario: A problem document carries its status, title and the path of the request
    When the API posts to "/item-prices" the body:
      """
      {"catalogItemId":"ghost","listPrice":{"amountMinor":1000,"currency":"BRL"}}
      """
    Then the API answers 422 with problem "unknown-catalog-item"
    And the response field "status" is 422
    And the response field "title" is "The catalogue item is not known to this service"
    And the response field "instance" is "/item-prices"
