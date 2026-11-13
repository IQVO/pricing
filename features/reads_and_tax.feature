# reads_and_tax.feature — wave 2: the tax table read (GET
# /tax-tables/current was at 0%), the item-price list with its paging rules,
# and the platform endpoints.
Feature: The tax table read and the item-price list

  Scenario: No tax table is current before the first publication
    When the API gets "/tax-tables/current"
    Then the API answers 404
    And the response field "type" is "/problems/not-found"

  Scenario: The current tax table is read back
    Given a tax rate of 1000 basis points everywhere from version 1
    When the API gets "/tax-tables/current"
    Then the API answers 200
    And the response field "version" is 1
    And the response field "state" is "CURRENT"
    Given a tax rate of 750 basis points everywhere from version 2
    When the API gets "/tax-tables/current"
    Then the API answers 200
    And the response field "version" is 2
    And the response field "bands.0.rateBasisPoints" is 750

  Scenario: Item prices are listed with the paging rules
    Given the catalogue item "i1" is known
    And the catalogue item "i2" is known
    And the catalogue item "i3" is known
    And the item "i1" is priced at 1000
    And the item "i2" is priced at 2000
    And the item "i3" is priced at 3000
    When the API gets "/item-prices"
    Then the API answers 200
    And the response field "items.0.catalogItemId" is "i1"
    When the API gets "/item-prices?limit=2"
    Then the API answers 200
    And the response field "items.0.catalogItemId" is "i1"
    And I remember the response field "nextCursor" as "page2"
    And the response field "items.1.catalogItemId" is "i2"
    When the API gets "/item-prices" with the remembered cursor "page2"
    Then the API answers 200
    And the response field "items.0.catalogItemId" is "i3"
    When the API gets "/item-prices?limit=0"
    Then the API answers 400
    And the response field "type" is "/problems/invalid-limit"
    When the API gets "/item-prices?cursor=not-a-cursor"
    Then the API answers 400
    And the response field "type" is "/problems/invalid-cursor"
    When the API gets "/item-prices?bogus=1"
    Then the API answers 400
    And the response field "type" is "/problems/unknown-query-parameter"

  Scenario: The platform endpoints answer
    When the API gets "/healthz"
    Then the API answers 200
    When the API gets "/readyz"
    Then the API answers 200
