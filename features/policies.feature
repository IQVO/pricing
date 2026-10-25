Feature: Merchandising publishes the delivery charge schedule and the tax table
  Exactly one schedule and one table are CURRENT. Publishing a higher version
  supersedes the previous one and publishes a change event.

  Scenario: A delivery charge schedule is published and becomes current
    When the API posts to "/delivery-charge-schedules" the body:
      """
      {"version":1,"tiers":[
        {"tier":"SAME_DAY","charge":{"amountMinor":2490,"currency":"BRL"}},
        {"tier":"NEXT_DAY","charge":{"amountMinor":1490,"currency":"BRL"},"freeOver":{"amountMinor":20000,"currency":"BRL"}},
        {"tier":"TWO_DAY","charge":{"amountMinor":990,"currency":"BRL"}},
        {"tier":"STANDARD","charge":{"amountMinor":0,"currency":"BRL"}}]}
      """
    Then the API answers 201
    And the response field "state" is "CURRENT"
    And 1 "DeliveryChargeScheduleChanged" events were published
    When the API gets "/delivery-charge-schedules/current"
    Then the response field "version" is 1

  Scenario: A schedule must name every tier
    When the API posts to "/delivery-charge-schedules" the body:
      """
      {"version":1,"tiers":[{"tier":"STANDARD","charge":{"amountMinor":0,"currency":"BRL"}}]}
      """
    Then the API answers 422 with problem "incomplete-charge-schedule"

  Scenario: A version that does not increase is refused
    Given every delivery tier charges 500 from version 3
    When the API posts to "/delivery-charge-schedules" the body:
      """
      {"version":3,"tiers":[
        {"tier":"SAME_DAY","charge":{"amountMinor":1,"currency":"BRL"}},
        {"tier":"NEXT_DAY","charge":{"amountMinor":1,"currency":"BRL"}},
        {"tier":"TWO_DAY","charge":{"amountMinor":1,"currency":"BRL"}},
        {"tier":"STANDARD","charge":{"amountMinor":1,"currency":"BRL"}}]}
      """
    Then the API answers 409 with problem "non-increasing-version"

  Scenario: No schedule is current before the first publication
    When the API gets "/delivery-charge-schedules/current"
    Then the API answers 404 with problem "not-found"

  Scenario: A tax table is published and overlapping bands are refused
    When the API posts to "/tax-tables" the body:
      """
      {"version":1,"bands":[{"postalPrefixFrom":"010","postalPrefixTo":"099","rateBasisPoints":1800}]}
      """
    Then the API answers 201
    And 1 "TaxTableChanged" events were published
    When the API posts to "/tax-tables" the body:
      """
      {"version":2,"bands":[
        {"postalPrefixFrom":"010","postalPrefixTo":"050","rateBasisPoints":100},
        {"postalPrefixFrom":"040","postalPrefixTo":"090","rateBasisPoints":100}]}
      """
    Then the API answers 422 with problem "overlapping-tax-band"
