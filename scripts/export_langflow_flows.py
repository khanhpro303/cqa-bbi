#!/usr/bin/env python3
"""Export the live chatbot flows from Langflow into langflow/flows/, secrets removed.

Read-only against Langflow (GET only). Run it after changing a flow in the
Langflow UI, then commit the diff so flow changes are reviewable.

    LANGFLOW_API_URL=https://lf.example.com LANGFLOW_API_KEY=sk-... \
        python3 scripts/export_langflow_flows.py [FLOW_ID ...]

Without FLOW_ID arguments it exports every flow whose name starts with
"BBI_RAG_Bot". Each file is written as langflow/flows/<name>.json.

Redaction: any template field that is a password/secret input, or whose name
ends in api_key/token/secret/password, keeps its value only when it is a
Langflow global-variable NAME (load_from_db=true). Literal values are blanked.
A final scan refuses to write a file that still looks like it holds a key.
"""
from __future__ import annotations

import json
import os
import re
import sys
import urllib.request
from pathlib import Path

OUT_DIR = Path(__file__).resolve().parent.parent / "langflow" / "flows"
SECRET_NAME = re.compile(r"(^|_)(api_key|token|secret|password)$", re.I)
SECRET_VALUE = re.compile(r"sk-[A-Za-z0-9_-]{10,}|AstraCS:[A-Za-z0-9:]+|eyJ[A-Za-z0-9_-]{20,}\.|Bearer\s+[A-Za-z0-9._-]{10,}")
# Langflow bookkeeping that changes on every save and only adds diff noise.
VOLATILE_KEYS = ("updated_at", "user_id", "folder_id", "webhook", "gradient", "icon_bg_color")


def get(url: str, key: str):
    req = urllib.request.Request(url, headers={"x-api-key": key, "Accept": "application/json"})
    with urllib.request.urlopen(req, timeout=30) as resp:
        return json.load(resp)


def redact_field(name: str, field: dict) -> None:
    if not isinstance(field, dict) or "value" not in field:
        return
    is_secret = field.get("password") or field.get("_input_type") == "SecretStrInput" or SECRET_NAME.search(name)
    if is_secret and isinstance(field["value"], str) and not field.get("load_from_db"):
        field["value"] = ""


def redact(obj):
    """Walk the whole flow: blank secret template fields, strip key-like strings."""
    if isinstance(obj, dict):
        if "template" in obj and isinstance(obj["template"], dict):
            for name, field in obj["template"].items():
                redact_field(name, field)
        return {k: redact(v) for k, v in obj.items()}
    if isinstance(obj, list):
        return [redact(v) for v in obj]
    if isinstance(obj, str) and SECRET_VALUE.search(obj):
        return SECRET_VALUE.sub("<redacted>", obj)
    return obj


def main(argv: list[str]) -> int:
    base = os.environ.get("LANGFLOW_API_URL", "").rstrip("/")
    key = os.environ.get("LANGFLOW_API_KEY", "")
    if not base or not key:
        print("Set LANGFLOW_API_URL and LANGFLOW_API_KEY", file=sys.stderr)
        return 2

    ids = argv[1:]
    if not ids:
        flows = get(f"{base}/api/v1/flows/?get_all=true&header_flows=true", key)
        ids = [f["id"] for f in flows if str(f.get("name", "")).startswith("BBI_RAG_Bot")]

    OUT_DIR.mkdir(parents=True, exist_ok=True)
    for flow_id in ids:
        flow = get(f"{base}/api/v1/flows/{flow_id}", key)
        for k in VOLATILE_KEYS:
            flow.pop(k, None)
        clean = redact(flow)
        text = json.dumps(clean, ensure_ascii=False, indent=2, sort_keys=True) + "\n"
        if SECRET_VALUE.search(text):
            print(f"refusing to write {flow.get('name')}: a secret survived redaction", file=sys.stderr)
            return 1
        path = OUT_DIR / f"{flow['name']}.json"
        path.write_text(text, encoding="utf-8")
        print(f"wrote {path.relative_to(OUT_DIR.parent.parent)} ({flow_id})")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
