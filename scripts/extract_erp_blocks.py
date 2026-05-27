#!/usr/bin/env python3
"""
Surgical extractor: remove two named blocks from backend/api/handlers/erp.go.

Each block is identified by its starting doc-comment line and the next
top-level definition that should remain. The script asserts the start
marker exists exactly once and the end marker exists exactly once after
it; otherwise it refuses to modify the file.

Run from repo root:
    python3 scripts/extract_erp_blocks.py
"""
from __future__ import annotations

import sys
from pathlib import Path

ERP_PATH = Path("backend/api/handlers/erp.go")

# (start_marker, end_marker_that_must_remain)
BLOCKS_TO_REMOVE = [
    (
        "// searchVariantsByAttributes returns child variants under a parent SKU",
        "func detectMaChaFromSearch(",
    ),
    (
        "// getAIClient creates an AI provider client based on tenant settings.",
        "func searchProductsByWebNameAstraDBNonVectorized(",
    ),
]


def find_unique_line(lines: list[str], needle: str) -> int:
    matches = [i for i, line in enumerate(lines) if line.startswith(needle)]
    if len(matches) != 1:
        msg = f"expected exactly 1 line starting with {needle!r}, got {len(matches)}"
        raise SystemExit(msg)
    return matches[0]


def main() -> int:
    text = ERP_PATH.read_text(encoding="utf-8")
    lines = text.splitlines(keepends=True)
    original_len = len(lines)

    # Walk in reverse so earlier indices stay valid as we splice later ones.
    spans: list[tuple[int, int]] = []
    for start_marker, end_marker in BLOCKS_TO_REMOVE:
        start = find_unique_line(lines, start_marker)
        end = find_unique_line(lines, end_marker)
        if end <= start:
            msg = f"end marker {end_marker!r} (line {end + 1}) is not after start {start_marker!r} (line {start + 1})"
            raise SystemExit(msg)
        spans.append((start, end))

    for start, end in sorted(spans, reverse=True):
        # Keep a single blank line where the block used to be so the next
        # surviving function is still separated from the previous one.
        del lines[start:end]

    new_text = "".join(lines)
    ERP_PATH.write_text(new_text, encoding="utf-8")

    removed = original_len - len(lines)
    print(f"[ok] Removed {removed} lines from {ERP_PATH} (now {len(lines)} lines).")
    return 0


if __name__ == "__main__":
    sys.exit(main())
