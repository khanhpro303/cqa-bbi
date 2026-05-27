#!/usr/bin/env python3
"""
One-shot updater for BBI_RAG_Bot_Ext.json.

Patches the ERPGatewayCaller custom component embedded in the Langflow flow:
  * adds color/size/brand input fields after parent_code
  * replaces the embedded Python source with the canonical
    ERPGatewayCaller.component.py file
  * keeps the rest of the flow untouched

Run from the repo root:
    python3 scripts/update_erp_gateway_flow.py
"""
from __future__ import annotations

import copy
import json
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
FLOW_PATH = REPO_ROOT / "BBI_RAG_Bot_Ext.json"
COMPONENT_PATH = REPO_ROOT / "ERPGatewayCaller.component.py"

NEW_FIELDS = [
    (
        "color",
        "Color",
        "Màu sắc/thuộc tính 1 khi resource=product_variants (vd: 'đen bóng', 'xanh navy'). Bỏ trống nếu user không nêu.",
    ),
    (
        "size",
        "Size",
        "Kích thước/thuộc tính 2 khi resource=product_variants (vd: 'L', 'XL'). Bỏ trống nếu user không nêu.",
    ),
    (
        "brand",
        "Brand",
        "Thương hiệu (tuỳ chọn, lọc theo nhãn hiệu khi cần).",
    ),
]


def find_erp_node(flow: dict) -> dict:
    for node in flow["data"]["nodes"]:
        if node.get("id") == "CustomComponent-EnSVr":
            return node
    msg = "ERPGatewayCaller node (CustomComponent-EnSVr) not found"
    raise SystemExit(msg)


def build_field_block(template: dict, name: str, display_name: str, info: str) -> dict:
    block = copy.deepcopy(template)
    block["name"] = name
    block["display_name"] = display_name
    block["info"] = info
    block["value"] = ""
    return block


def main() -> int:
    flow = json.loads(FLOW_PATH.read_text(encoding="utf-8"))
    new_code = COMPONENT_PATH.read_text(encoding="utf-8")

    node = find_erp_node(flow)
    node_data = node["data"]["node"]
    template = node_data["template"]

    # 1. Update field_order — insert color/size/brand right after parent_code
    field_order: list[str] = list(node_data["field_order"])
    for name, _, _ in NEW_FIELDS:
        if name in field_order:
            field_order.remove(name)
    insert_at = field_order.index("parent_code") + 1
    for offset, (name, _, _) in enumerate(NEW_FIELDS):
        field_order.insert(insert_at + offset, name)
    node_data["field_order"] = field_order

    # 2. Replace embedded component source with current .py file
    if "code" not in template:
        msg = "code field missing from ERPGatewayCaller template"
        raise SystemExit(msg)
    template["code"]["value"] = new_code

    # 3. Add new field config blocks modeled on parent_code
    parent_block = template["parent_code"]
    for name, display_name, info in NEW_FIELDS:
        template[name] = build_field_block(parent_block, name, display_name, info)

    FLOW_PATH.write_text(
        json.dumps(flow, indent=2, ensure_ascii=False) + "\n",
        encoding="utf-8",
    )
    print(f"[ok] Patched {FLOW_PATH.name}: field_order={field_order}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
