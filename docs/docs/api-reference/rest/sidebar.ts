import type { SidebarsConfig } from "@docusaurus/plugin-content-docs";

const sidebar: SidebarsConfig = {
  apisidebar: [
    {
      type: "doc",
      id: "api-reference/rest/pricing-api",
    },
    {
      type: "category",
      label: "ItemPrices",
      link: {
        type: "doc",
        id: "api-reference/rest/item-prices",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/create-item-price",
          label: "Create the price record of a catalogue item",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/list-item-prices",
          label: "List item prices, keyset-paged (projection bootstrap)",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-item-price",
          label: "Get the price record of one catalogue item",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/set-list-price",
          label: "Set the list price of a catalogue item",
          className: "api-method put",
        },
        {
          type: "doc",
          id: "api-reference/rest/withdraw-price",
          label: "Withdraw the price of a catalogue item",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/schedule-sale",
          label: "Schedule a sale window for a catalogue item",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/cancel-sale",
          label: "Cancel a scheduled sale window",
          className: "api-method post",
        },
      ],
    },
    {
      type: "category",
      label: "DeliveryCharges",
      link: {
        type: "doc",
        id: "api-reference/rest/delivery-charges",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/publish-charge-schedule",
          label: "Publish a new delivery charge schedule",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-current-charge-schedule",
          label: "Get the current delivery charge schedule",
          className: "api-method get",
        },
      ],
    },
    {
      type: "category",
      label: "TaxTables",
      link: {
        type: "doc",
        id: "api-reference/rest/tax-tables",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/publish-tax-table",
          label: "Publish a new tax table",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-current-tax-table",
          label: "Get the current tax table",
          className: "api-method get",
        },
      ],
    },
    {
      type: "category",
      label: "PriceQuotes",
      link: {
        type: "doc",
        id: "api-reference/rest/price-quotes",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/issue-price-quote",
          label: "Issue a binding price quote for a checkout",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-price-quote",
          label: "Get a price quote",
          className: "api-method get",
        },
      ],
    },
  ],
};

export default sidebar.apisidebar;
