# tax_table_rules.feature — wave 3: TaxTable rules from the documentation
# (aggregate-design-canvas, use-cases PublishTaxTable, ADR 0002 A9 and A18,
# troubleshooting rate-out-of-range / overlapping-tax-band / validation-failed).
Feature: A tax table has disjoint closed bands, bounded rates and supersedes, never edits
  Bands are closed ranges of 3-character postal prefixes compared as text; a
  rate is 0 to 10000 basis points. Exactly one table is CURRENT.

  Scenario: A new version supersedes the current table and the history is kept
    Given a tax rate of 1000 basis points everywhere from version 1
    And a tax rate of 750 basis points everywhere from version 2
    When the API gets "/tax-tables/current"
    Then the response field "version" is 2
    And the response field "state" is "CURRENT"
    And the store holds 2 tax tables
    And 2 "TaxTableChanged" events were published
    And the latest "TaxTableChanged" event carries version 2 and is keyed by "tax-2"

  Scenario: A version that does not exceed the current one is refused and nothing changes
    Given a tax rate of 1000 basis points everywhere from version 5
    When the API posts to "/tax-tables" the body:
      """
      {"version":4,"bands":[{"postalPrefixFrom":"000","postalPrefixTo":"ZZZ","rateBasisPoints":1}]}
      """
    Then the API answers 409 with problem "non-increasing-version"
    When the API posts to "/tax-tables" the body:
      """
      {"version":5,"bands":[{"postalPrefixFrom":"000","postalPrefixTo":"ZZZ","rateBasisPoints":1}]}
      """
    Then the API answers 409 with problem "non-increasing-version"
    When the API gets "/tax-tables/current"
    Then the response field "version" is 5
    And the response field "bands.0.rateBasisPoints" is 1000
    And the store holds 1 tax tables
    And 1 "TaxTableChanged" events were published

  Scenario Outline: A rate is between 0 and 10000 basis points
    When the API posts to "/tax-tables" the body:
      """
      {"version":1,"bands":[{"postalPrefixFrom":"000","postalPrefixTo":"ZZZ","rateBasisPoints":<rate>}]}
      """
    Then the API answers 201

    Examples: the lowest and the highest rate
      | rate  |
      | 0     |
      | 10000 |

  Scenario Outline: A rate outside 0 to 10000 basis points is refused
    When the API posts to "/tax-tables" the body:
      """
      {"version":1,"bands":[{"postalPrefixFrom":"000","postalPrefixTo":"ZZZ","rateBasisPoints":<rate>}]}
      """
    Then the API answers 422 with problem "rate-out-of-range"
    And the store holds 0 tax tables

    Examples: one below the lowest and one above the highest rate
      | rate  |
      | -1    |
      | 10001 |

  Scenario: Bands are closed ranges: sharing the edge prefix overlaps and the next prefix does not
    When the API posts to "/tax-tables" the body:
      """
      {"version":1,"bands":[
        {"postalPrefixFrom":"010","postalPrefixTo":"050","rateBasisPoints":100},
        {"postalPrefixFrom":"050","postalPrefixTo":"099","rateBasisPoints":200}]}
      """
    Then the API answers 422 with problem "overlapping-tax-band"
    When the API posts to "/tax-tables" the body:
      """
      {"version":1,"bands":[
        {"postalPrefixFrom":"010","postalPrefixTo":"050","rateBasisPoints":100},
        {"postalPrefixFrom":"051","postalPrefixTo":"099","rateBasisPoints":200}]}
      """
    Then the API answers 201

  Scenario: Bands may be sent in any order and are served lowest first
    When the API posts to "/tax-tables" the body:
      """
      {"version":1,"bands":[
        {"postalPrefixFrom":"500","postalPrefixTo":"599","rateBasisPoints":200},
        {"postalPrefixFrom":"010","postalPrefixTo":"099","rateBasisPoints":100}]}
      """
    Then the API answers 201
    When the API gets "/tax-tables/current"
    Then the response field "bands.0.postalPrefixFrom" is "010"
    And the response field "bands.1.postalPrefixFrom" is "500"

  Scenario Outline: A postal prefix is exactly 3 upper-case letters or digits
    When the API posts to "/tax-tables" the body:
      """
      {"version":1,"bands":[{"postalPrefixFrom":"<from>","postalPrefixTo":"099","rateBasisPoints":100}]}
      """
    Then the API answers 422 with problem "validation-failed"

    Examples: lower case, too short, too long, a character outside the alphabet
      | from |
      | abc  |
      | 01   |
      | 0100 |
      | 0-1  |

  Scenario: A prefix of letters and digits is a valid band edge
    When the API posts to "/tax-tables" the body:
      """
      {"version":1,"bands":[{"postalPrefixFrom":"A1B","postalPrefixTo":"Z9Z","rateBasisPoints":100}]}
      """
    Then the API answers 201

  Scenario: A band that ends before it starts and an empty table are refused
    When the API posts to "/tax-tables" the body:
      """
      {"version":1,"bands":[{"postalPrefixFrom":"099","postalPrefixTo":"010","rateBasisPoints":100}]}
      """
    Then the API answers 422 with problem "validation-failed"
    When the API posts to "/tax-tables" the body:
      """
      {"version":1,"bands":[]}
      """
    Then the API answers 422 with problem "validation-failed"

  Scenario: A table holds at most 500 bands
    When the API publishes a tax table of version 1 with 500 one-prefix bands of 100 basis points
    Then the API answers 201
    When the API publishes a tax table of version 2 with 501 one-prefix bands of 100 basis points
    Then the API answers 422 with problem "validation-failed"
    When the API gets "/tax-tables/current"
    Then the response field "version" is 1
