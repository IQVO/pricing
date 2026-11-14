# catalogue_feed_rules.feature — wave 3: the CatalogItemView projector and the
# WithdrawOnDiscontinue policy (use-cases "Project CatalogItemView" and
# "WithdrawPrice (policy WithdrawOnDiscontinue)", domain-events "Consumed",
# ADR 0002 A2, eventstorming hotspot H6).
Feature: The catalogue feed from offer-availability gates pricing and withdraws discontinued items
  A price can only be set for an item the view knows and has not seen
  discontinued. Every consumed event is applied once, by its CloudEvents id.

  Scenario: Discontinuing an item that was never priced only updates the view
    Given the catalogue item "sku-9" is known
    When offer-availability discontinues the item "sku-9"
    Then 0 "ItemPriceWithdrawn" events were published
    When the API posts to "/item-prices" the body:
      """
      {"catalogItemId":"sku-9","listPrice":{"amountMinor":1000,"currency":"BRL"}}
      """
    Then the API answers 422 with problem "unknown-catalog-item"

  Scenario: A late or replayed creation never revives a discontinued item
    Given the catalogue item "sku-9" is known
    And offer-availability discontinues the item "sku-9"
    When offer-availability delivers the event "ev-late" that creates the item "sku-9"
    Then the consumer acknowledged the event
    When the API posts to "/item-prices" the body:
      """
      {"catalogItemId":"sku-9","listPrice":{"amountMinor":1000,"currency":"BRL"}}
      """
    Then the API answers 422 with problem "unknown-catalog-item"

  Scenario: A price set before the creation event arrived succeeds once it has
    When the API posts to "/item-prices" the body:
      """
      {"catalogItemId":"sku-new","listPrice":{"amountMinor":1000,"currency":"BRL"}}
      """
    Then the API answers 422 with problem "unknown-catalog-item"
    When offer-availability delivers the event "ev-created" that creates the item "sku-new"
    And the API posts to "/item-prices" the body:
      """
      {"catalogItemId":"sku-new","listPrice":{"amountMinor":1000,"currency":"BRL"}}
      """
    Then the API answers 201

  Scenario: A CloudEvents id is applied once
    Given the item "sku-1" is priced at 1000
    And the item "sku-2" is priced at 2000
    When offer-availability delivers the event "ev-dup" that discontinues the item "sku-1"
    Then the consumer acknowledged the event
    When offer-availability delivers the event "ev-dup" that discontinues the item "sku-2"
    Then the consumer acknowledged the event
    When the API gets "/item-prices/sku-1"
    Then the response field "state" is "WITHDRAWN"
    When the API gets "/item-prices/sku-2"
    Then the response field "state" is "ACTIVE"
    And 1 "ItemPriceWithdrawn" events were published

  Scenario: Discontinuing an item whose price is already withdrawn publishes no second withdrawal
    Given the item "sku-1" is priced at 1000
    And the API posts to "/item-prices/sku-1/withdrawals" with no body
    When offer-availability delivers the event "ev-disc-1" that discontinues the item "sku-1"
    Then the consumer acknowledged the event
    And 1 "ItemPriceWithdrawn" events were published

  Scenario: The withdrawal names the item and the version of the withdrawn price
    Given the item "sku-1" is priced at 1000
    When offer-availability discontinues the item "sku-1"
    Then the latest "ItemPriceWithdrawn" event carries version 2 and is keyed by "sku-1"

  Scenario: An event with an invalid catalogItemId can never apply and projects nothing
    When offer-availability delivers the event "ev-bad-1" that creates the item "not a valid id"
    Then the consumer refused the event as invalid
    When offer-availability delivers the event "ev-bad-2" that discontinues the item "not a valid id"
    Then the consumer refused the event as invalid
    And 0 "ItemPriceWithdrawn" events were published
