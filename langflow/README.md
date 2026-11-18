# Langflow flows (source of truth in git)

The Zalo chatbot runs two flows on the self-hosted Langflow instance. The active
flow IDs are set on the app's **AI Engine** page, not here.

| Flow | Used for | Langflow ID |
| --- | --- | --- |
| `BBI_RAG_Bot_Int` | private bot (whitelisted staff, 1:1) | `4dd93268…` |
| `BBI_RAG_Bot_Ext` | public bot (customers, groups) | `37593e48…` |

- `flows/` — exports of the live flows with secrets blanked (global-variable
  names such as `OPENAI_API_KEY` are kept). Refresh after every change made in
  the Langflow UI and commit the diff:

  ```bash
  LANGFLOW_API_URL=https://lf.kariendoan.xyz LANGFLOW_API_KEY=... \
      python3 scripts/export_langflow_flows.py
  ```

- `components/ERPGatewayCaller.py` — the code of the "ERP Gateway Caller" custom
  component. Langflow keeps its own copy inside each flow; when you change this
  file, paste it into the node in **both** flows, then re-export.

Never commit a raw `GET /api/v1/flows/{id}` dump: custom MCP and AstraDB nodes
can hold literal keys. Always go through the export script.
