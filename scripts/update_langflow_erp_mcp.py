#!/usr/bin/env python3
"""Idempotently replace the legacy ERP Caller with native Langflow MCP Tools.

Dry-run is the default. Use --apply to register the MCP transports and patch the
two flow graphs. Use --verify after deployment to prove graph wiring and tool
discovery without invoking an ERP data tool.
"""
from __future__ import annotations

import argparse
import copy
import gzip
import hashlib
import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request
import zlib
from typing import Any

SERVER_PREFIX = "cqa-erp"
PERMISSION_VARIABLE = "ERP_PERMISSION_TOKEN"
# CQA sends the agent token per request (X-Langflow-Global-Var-CQA_ERP_AGENT_TOKEN).
# A stored-variable name here would be sent literally: Langflow stops loading
# stored globals once a request carries any X-Langflow-Global-Var-* header.
AGENT_TOKEN_VARIABLE = "CQA_ERP_AGENT_TOKEN"
EXPECTED_TOOLS = 7


class UpdateError(RuntimeError):
    pass


class LangflowAPI:
    def __init__(self, base_url: str, api_key: str) -> None:
        self.base_url = base_url.rstrip("/")
        self.api_key = api_key

    def request(self, method: str, path: str, payload: dict[str, Any] | None = None) -> Any:
        body = None if payload is None else json.dumps(payload).encode()
        req = urllib.request.Request(
            self.base_url + path,
            data=body,
            method=method,
            headers={"x-api-key": self.api_key, "Content-Type": "application/json"},
        )
        try:
            with urllib.request.urlopen(req, timeout=45) as response:
                raw = response.read()
                encoding = (response.headers.get("Content-Encoding") or "").lower()
        except urllib.error.HTTPError as exc:
            raise UpdateError(f"Langflow {method} {path} failed with HTTP {exc.code}") from exc
        except urllib.error.URLError as exc:
            raise UpdateError(f"Langflow {method} {path} is unavailable") from exc
        if not raw:
            return None
        if encoding == "gzip":
            raw = gzip.decompress(raw)
        elif encoding == "deflate":
            raw = zlib.decompress(raw)
        try:
            return json.loads(raw)
        except json.JSONDecodeError as exc:
            raise UpdateError(f"Langflow {method} {path} returned invalid JSON") from exc


def find_mcp_component(value: Any) -> dict[str, Any] | None:
    if isinstance(value, dict):
        template = value.get("template")
        if value.get("display_name") == "MCP Tools" and isinstance(template, dict) and "mcp_server" in template:
            return value
        for child in value.values():
            found = find_mcp_component(child)
            if found:
                return found
    elif isinstance(value, list):
        for child in value:
            found = find_mcp_component(child)
            if found:
                return found
    return None


def legacy_erp_node(node: dict[str, Any]) -> bool:
    definition = node.get("data", {}).get("node", {})
    if definition.get("display_name") == "ERP Gateway Caller":
        return True
    code = definition.get("template", {}).get("code", {}).get("value", "")
    return isinstance(code, str) and "class ERPGatewayCaller" in code


def token_variable_from_legacy(node: dict[str, Any]) -> str:
    field = node.get("data", {}).get("node", {}).get("template", {}).get("secure_token", {})
    value = field.get("value")
    if field.get("load_from_db") is not True or not isinstance(value, str) or not value.strip():
        raise UpdateError("legacy ERP token must reference a Langflow credential global variable")
    return value.strip()


def server_name(flow_name: str, flow_id: str) -> str:
    label = "public" if "ext" in flow_name.lower() or "public" in flow_name.lower() else "private"
    return f"{SERVER_PREFIX}-{label}-{flow_id[:8]}"


def desired_server_config(mcp_url: str, agent_token_variable: str) -> dict[str, Any]:
    return {
        "url": mcp_url,
        "headers": {
            "X-Agent-Token": agent_token_variable,
            "X-Permission-Token": PERMISSION_VARIABLE,
        },
    }


def encode_handle(handle: dict[str, Any]) -> str:
    return json.dumps(handle, ensure_ascii=False, separators=(",", ":")).replace('"', "œ")


def mcp_node_id(flow_id: str) -> str:
    return "MCPTools-cqa" + hashlib.sha256(flow_id.encode()).hexdigest()[:7]


def patch_graph(
    flow: dict[str, Any], component_template: dict[str, Any], server: str, config: dict[str, Any]
) -> tuple[dict[str, Any], bool]:
    patched = copy.deepcopy(flow)
    graph = patched.get("data")
    if not isinstance(graph, dict):
        raise UpdateError("flow graph data is missing")
    nodes = graph.get("nodes") or []
    edges = graph.get("edges") or []
    legacy = [node for node in nodes if legacy_erp_node(node)]
    existing = [
        node
        for node in nodes
        if node.get("data", {}).get("type") == "MCPTools"
        and node.get("data", {}).get("node", {}).get("mcpServerName") == server
    ]
    if len(legacy) > 1 or len(existing) > 1:
        raise UpdateError("flow contains duplicate ERP tool nodes")

    node_id = existing[0].get("id") if existing else mcp_node_id(str(flow.get("id", "")))
    old_id = legacy[0].get("id") if legacy else None
    old_tool_edges = [
        edge
        for edge in edges
        if edge.get("source") == old_id
        and edge.get("data", {}).get("targetHandle", {}).get("fieldName") == "tools"
    ]
    current_edges = [
        edge
        for edge in edges
        if edge.get("source") == node_id
        and edge.get("data", {}).get("targetHandle", {}).get("fieldName") == "tools"
    ]
    if not current_edges and len(old_tool_edges) != 1:
        raise UpdateError("expected exactly one legacy ERP-to-Agent tools edge")

    definition = copy.deepcopy(component_template)
    definition["mcpServerName"] = server
    definition["key"] = server.replace("-", "_")
    template = definition.setdefault("template", {})
    template.setdefault("mcp_server", {})["value"] = {"name": server, "config": config}
    template.setdefault("use_cache", {})["value"] = False
    template.setdefault("verify_ssl", {})["value"] = True
    template.setdefault("tool", {})["value"] = ""
    template.setdefault("tool_placeholder", {})["tool_mode"] = True
    if isinstance(template.get("tools_metadata"), dict):
        template["tools_metadata"]["show"] = True
    for output in definition.get("outputs", []):
        if output.get("name") == "component_as_tool":
            output["cache"] = False
            output["selected"] = "Tool"
            output["tool_mode"] = True

    if existing:
        new_node = copy.deepcopy(existing[0])
        new_node.setdefault("data", {})["node"] = definition
    else:
        new_node = {
            "id": node_id,
            "type": "genericNode",
            "position": copy.deepcopy(legacy[0].get("position", {"x": 0, "y": 0})),
            "selected": False,
            "data": {"id": node_id, "type": "MCPTools", "node": definition},
        }

    retained_nodes = [node for node in nodes if node.get("id") not in {old_id, node_id}]
    retained_nodes.append(new_node)
    retained_edges = [
        edge for edge in edges if edge.get("source") not in {old_id, node_id} and edge.get("target") not in {old_id, node_id}
    ]
    target_edge = current_edges[0] if current_edges else old_tool_edges[0]
    target = target_edge.get("target")
    target_handle = copy.deepcopy(target_edge.get("data", {}).get("targetHandle"))
    if not target or not isinstance(target_handle, dict):
        raise UpdateError("Agent tools edge is malformed")
    source_handle = {"dataType": "MCPTools", "id": node_id, "name": "component_as_tool", "output_types": ["Tool"]}
    source_encoded = encode_handle(source_handle)
    target_encoded = target_edge.get("targetHandle") or encode_handle(target_handle)
    new_edge = {
        "id": f"xy-edge__{node_id}{source_encoded}-{target}{target_encoded}",
        "source": node_id,
        "sourceHandle": source_encoded,
        "target": target,
        "targetHandle": target_encoded,
        "selected": False,
        "animated": False,
        "className": "",
        "data": {"sourceHandle": source_handle, "targetHandle": target_handle},
    }
    retained_edges.append(new_edge)
    graph["nodes"] = retained_nodes
    graph["edges"] = retained_edges
    changed = graph != flow.get("data")
    return patched, changed


def load_flow(api: LangflowAPI, flow_id: str) -> dict[str, Any]:
    flow = api.request("GET", f"/api/v1/flows/{urllib.parse.quote(flow_id)}")
    if not isinstance(flow, dict):
        raise UpdateError(f"flow {flow_id} was not found")
    return flow


def register_server(api: LangflowAPI, name: str, config: dict[str, Any], apply: bool) -> bool:
    existing = api.request("GET", f"/api/v2/mcp/servers/{urllib.parse.quote(name)}")
    if isinstance(existing, dict) and existing.get("url") == config["url"] and existing.get("headers") == config["headers"]:
        return False
    if apply:
        method = "PATCH" if existing else "POST"
        api.request(method, f"/api/v2/mcp/servers/{urllib.parse.quote(name)}", config)
    return True


def discover_tool_count(api: LangflowAPI, name: str) -> int:
    rows = api.request("GET", "/api/v2/mcp/servers?action_count=true")
    for row in rows or []:
        if row.get("name") == name:
            if row.get("error"):
                raise UpdateError(f"MCP server {name} tool discovery failed")
            return int(row.get("toolsCount") or 0)
    raise UpdateError(f"MCP server {name} is not registered")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument("--apply", action="store_true")
    mode.add_argument("--verify", action="store_true")
    parser.add_argument("--tenant-id", required=True)
    parser.add_argument("--gateway-base-url", required=True)
    parser.add_argument("flow_ids", nargs="+")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    api_url = os.environ.get("LANGFLOW_API_URL", "").strip()
    api_key = os.environ.get("LANGFLOW_API_KEY", "").strip()
    if not api_url or not api_key:
        print("LANGFLOW_API_URL and LANGFLOW_API_KEY are required", file=sys.stderr)
        return 2
    api = LangflowAPI(api_url, api_key)
    registry = api.request("GET", "/api/v1/all")
    component = find_mcp_component(registry)
    if not component:
        raise UpdateError("MCP Tools is unavailable in this Langflow version")
    mcp_url = f"{args.gateway_base_url.rstrip('/')}/mcp/erp/{urllib.parse.quote(args.tenant_id)}"

    changed = False
    planned: list[tuple[str, str]] = []
    for flow_id in args.flow_ids:
        flow = load_flow(api, flow_id)
        token_variable = AGENT_TOKEN_VARIABLE
        name = server_name(str(flow.get("name", "")), flow_id)
        config = desired_server_config(mcp_url, token_variable)
        server_changed = register_server(api, name, config, args.apply)
        patched, graph_changed = patch_graph(flow, component, name, config)
        changed = changed or server_changed or graph_changed
        planned.append((flow_id, name))
        if args.apply and graph_changed:
            api.request("PATCH", f"/api/v1/flows/{urllib.parse.quote(flow_id)}", {"data": patched["data"]})
        print(f"{flow.get('name', flow_id)}: server={'change' if server_changed else 'ok'}, graph={'change' if graph_changed else 'ok'}")

    if args.verify:
        if changed:
            raise UpdateError("verification found configuration drift")
        for _, name in planned:
            count = discover_tool_count(api, name)
            if count != EXPECTED_TOOLS:
                raise UpdateError(f"MCP server {name} exposes {count} tools; expected {EXPECTED_TOOLS}")
            print(f"{name}: {count} tools")
    elif not args.apply:
        print("dry-run only; rerun with --apply after the ERP MCP endpoint is deployed")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except UpdateError as exc:
        print(f"error: {exc}", file=sys.stderr)
        raise SystemExit(1) from exc
