import copy
import unittest

from update_langflow_erp_mcp import (
    desired_server_config,
    legacy_erp_node,
    patch_graph,
    register_server,
    token_variable_from_legacy,
)


def component():
    return {
        "display_name": "MCP Tools",
        "outputs": [{"name": "component_as_tool", "cache": True}],
        "template": {
            "mcp_server": {"value": None},
            "tool": {"value": None},
            "tool_placeholder": {"tool_mode": False},
        },
    }


def flow():
    target_handle = {"fieldName": "tools", "id": "Agent-1", "inputTypes": ["Tool"], "type": "other"}
    legacy = {
        "id": "CustomComponent-1",
        "type": "genericNode",
        "position": {"x": 10, "y": 20},
        "data": {
            "type": "CustomComponent",
            "node": {
                "display_name": "ERP Gateway Caller",
                "template": {"secure_token": {"value": "ERP_PRIVATE_TOKEN", "load_from_db": True}},
            },
        },
    }
    return {
        "id": "flow-1",
        "name": "BBI_RAG_Bot_Int",
        "data": {
            "nodes": [legacy, {"id": "Agent-1", "data": {"type": "Agent"}}],
            "edges": [
                {
                    "id": "old",
                    "source": "CustomComponent-1",
                    "target": "Agent-1",
                    "targetHandle": "encoded-target",
                    "data": {"targetHandle": target_handle},
                }
            ],
        },
    }


class GraphPatchTest(unittest.TestCase):
    def test_replaces_legacy_node_and_is_idempotent(self):
        original = flow()
        config = desired_server_config("https://cqa.example/mcp/erp/t1", "ERP_PRIVATE_TOKEN")
        patched, changed = patch_graph(original, component(), "cqa-erp-private-flow", config)
        self.assertTrue(changed)
        self.assertEqual(original, flow())
        self.assertFalse(any(legacy_erp_node(node) for node in patched["data"]["nodes"]))
        mcp = [node for node in patched["data"]["nodes"] if node["data"].get("type") == "MCPTools"]
        self.assertEqual(len(mcp), 1)
        self.assertEqual(mcp[0]["data"]["node"]["template"]["mcp_server"]["value"]["config"], config)
        self.assertEqual(patched["data"]["edges"][0]["target"], "Agent-1")

        again, changed_again = patch_graph(copy.deepcopy(patched), component(), "cqa-erp-private-flow", config)
        self.assertFalse(changed_again)
        self.assertEqual(again, patched)

    def test_requires_credential_global_variable(self):
        node = flow()["data"]["nodes"][0]
        node["data"]["node"]["template"]["secure_token"]["load_from_db"] = False
        with self.assertRaisesRegex(RuntimeError, "credential global variable"):
            token_variable_from_legacy(node)

    def test_server_registration_is_idempotent_with_langflow_defaults(self):
        config = desired_server_config("https://cqa.example/mcp/erp/t1", "ERP_PRIVATE_TOKEN")

        class API:
            def __init__(self):
                self.calls = []

            def request(self, method, path, payload=None):
                self.calls.append((method, path, payload))
                if method == "GET":
                    return {**config, "command": None, "args": None, "env": None}
                raise AssertionError("idempotent registration must not mutate")

        api = API()
        self.assertFalse(register_server(api, "cqa-erp-private-flow", config, apply=True))
        self.assertEqual(len(api.calls), 1)


if __name__ == "__main__":
    unittest.main()
