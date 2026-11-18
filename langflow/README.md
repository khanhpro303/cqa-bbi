# Langflow flows (source of truth in git)

The Zalo chatbot runs two flows on the self-hosted Langflow instance. The active
flow IDs are set on the app's **AI Engine** page, not here.

| Flow | Used for | Langflow ID |
| --- | --- | --- |
| `BBI_RAG_Bot_Int` | private bot (whitelisted staff, 1:1) | `4dd93268-5540-4617-8998-4ba78d2d6d18` |
| `BBI_RAG_Bot_Ext` | public bot (customers, groups) | `37593e48-052f-4be3-abcc-94a21465b3aa` |

- `flows/` — exports of the live flows with secrets blanked (global-variable
  names such as `OPENAI_API_KEY` are kept). Refresh after every change made in
  the Langflow UI and commit the diff:

  ```bash
  LANGFLOW_API_URL=https://lf.kariendoan.xyz LANGFLOW_API_KEY=... \
      python3 scripts/export_langflow_flows.py
  ```

- `components/ERPGatewayCaller.py` — the code of the "ERP Gateway Caller" custom
  component used before the ERP MCP migration. Keep it only while an older flow
  export still references the node. New flows must use the native MCP Tools
  component described below.

## ERP MCP Tools

Both `BBI_RAG_Bot_Int` and `BBI_RAG_Bot_Ext` connect their Agent `Tools` input
to one native **MCP Tools** component. Register the server in Langflow with:

```json
{
  "mcpServers": {
    "cqa-erp": {
      "url": "https://cqa.kariendoan.xyz/mcp/erp/<tenant-id>",
      "headers": {
        "X-Agent-Token": "CQA_ERP_AGENT_TOKEN",
        "X-Permission-Token": "ERP_PERMISSION_TOKEN"
      }
    }
  }
}
```

`CQA_ERP_AGENT_TOKEN` is a Langflow credential global variable. CQA sends the
request-specific `ERP_PERMISSION_TOKEN` to the Langflow `/run` endpoint in the
`X-Langflow-Global-Var-ERP_PERMISSION_TOKEN` HTTP header. Do not expose either
value as a Tool Mode input, prompt variable, or model-selected tool argument.

Leave the MCP Tools `Tool` field empty and enable Tool Mode. The Agent then sees
the typed tools exported by CQA and chooses between them from their schemas:

- `search_products` for product-line discovery, price intent, and web-name
  disambiguation.
- `search_product_variants` after a product parent has been resolved.
- `inventory_by_sku` only for an exact SKU or a concrete colour/size variant.
- `inventory_by_line` when the customer asks for stock of a whole named line.
- `find_customer`, `lookup_order`, and `lookup_debt` for customer-specific ERP
  questions.

The Agent must copy identifiers returned by tools. It must not invent parent
codes, SKUs, customer codes, endpoint paths, HTTP methods, warehouse names, or
tenant IDs. Inventory tool results use the backend's fixed `1.KHO-TONG` report
semantics.

Never commit a raw `GET /api/v1/flows/{id}` dump: custom MCP and AstraDB nodes
can hold literal keys. Always go through the export script.
