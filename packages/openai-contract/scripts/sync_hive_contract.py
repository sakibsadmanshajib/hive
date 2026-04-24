#!/usr/bin/env python3

from __future__ import annotations

import copy
import json
from pathlib import Path

import yaml


HTTP_METHODS = ("get", "put", "post", "delete", "options", "head", "patch", "trace")
STATUS_ORDER = (
    "supported_now",
    "planned_for_launch",
    "explicitly_unsupported_at_launch",
    "out_of_scope",
)
SERVER_CONFIG = [
    {
        "url": "/v1",
        "description": "Hive API base URL on the current host",
    }
]
PROVENANCE_LINE = (
    "Generated from `packages/openai-contract/matrix/support-matrix.json`. "
    "Do not edit manually."
)
UPSTREAM_BASE_URL = "https://api.openai.com/v1"
SCRIPT_DIR = Path(__file__).resolve().parent
REPO_ROOT = SCRIPT_DIR.parents[2]


def load_matrix(matrix_path: Path) -> tuple[dict, dict[str, dict]]:
    matrix_document = json.loads(Path(matrix_path).read_text(encoding="utf-8"))
    matrix_lookup: dict[str, dict] = {}

    for endpoint in matrix_document.get("endpoints", []):
        matrix_lookup[f"{endpoint['method'].upper()} {endpoint['path']}"] = endpoint

    return matrix_document, matrix_lookup


def render_openapi(
    upstream_spec_path: Path,
    matrix_lookup: dict[str, dict],
    output_path: Path,
) -> int:
    upstream_spec = yaml.safe_load(Path(upstream_spec_path).read_text(encoding="utf-8"))
    upstream_paths = upstream_spec.get("paths", {})
    rendered_paths: dict[str, dict] = {}
    public_operations = 0

    for upstream_path, path_item in upstream_paths.items():
        rendered_path_item: dict[str, object] = {}

        for key, value in path_item.items():
            method = key.lower()
            if method not in HTTP_METHODS:
                rendered_path_item[key] = copy.deepcopy(value)
                continue

            matrix_key = f"{method.upper()} /v1{upstream_path}"
            matrix_entry = matrix_lookup.get(matrix_key)
            if matrix_entry is None:
                raise KeyError(f"support-matrix.json is missing {matrix_key}")
            if matrix_entry["status"] == "out_of_scope":
                continue

            operation = copy.deepcopy(value)
            operation["x-hive-status"] = matrix_entry["status"]
            operation["x-hive-phase"] = matrix_entry.get("phase")
            operation["x-hive-notes"] = matrix_entry.get("notes")
            rendered_path_item[key] = operation
            public_operations += 1

        if any(key.lower() in HTTP_METHODS for key in rendered_path_item):
            rendered_paths[upstream_path] = rendered_path_item

    upstream_spec["servers"] = copy.deepcopy(SERVER_CONFIG)
    upstream_spec["paths"] = rendered_paths
    upstream_spec.pop("x-oaiMeta", None)
    upstream_spec = replace_upstream_base_url(upstream_spec)

    output_path = Path(output_path)
    output_path.parent.mkdir(parents=True, exist_ok=True)
    output_path.write_text(
        yaml.safe_dump(
            upstream_spec,
            allow_unicode=False,
            sort_keys=False,
        ),
        encoding="utf-8",
    )

    return public_operations


def replace_upstream_base_url(value):
    if isinstance(value, dict):
        return {key: replace_upstream_base_url(item) for key, item in value.items()}
    if isinstance(value, list):
        return [replace_upstream_base_url(item) for item in value]
    if isinstance(value, str):
        return value.replace(UPSTREAM_BASE_URL, "/v1")
    return value


def render_markdown(matrix_document: dict, output_path: Path) -> None:
    sections: dict[str, list[dict]] = {status: [] for status in STATUS_ORDER}

    for endpoint in matrix_document.get("endpoints", []):
        sections.setdefault(endpoint["status"], []).append(endpoint)

    lines = [
        "# Hive API Support Matrix",
        "",
        PROVENANCE_LINE,
        "",
    ]

    for status in STATUS_ORDER:
        endpoints = sections.get(status, [])
        lines.extend(
            [
                f"## {status} ({len(endpoints)} endpoints)",
                "",
                "| Method | Path | Status | Phase | Notes |",
                "|--------|------|--------|-------|-------|",
            ]
        )

        for endpoint in endpoints:
            phase = endpoint["phase"] if endpoint.get("phase") is not None else "--"
            notes = endpoint.get("notes", "").replace("\n", " ").replace("|", "\\|").strip()
            lines.append(
                f"| {endpoint['method'].upper()} | `{endpoint['path']}` | "
                f"{endpoint['status']} | {phase} | {notes} |"
            )

        lines.extend(["", ""])

    output_path = Path(output_path)
    output_path.parent.mkdir(parents=True, exist_ok=True)
    output_path.write_text("\n".join(lines).rstrip() + "\n", encoding="utf-8")


def main() -> int:
    matrix_path = REPO_ROOT / "packages/openai-contract/matrix/support-matrix.json"
    upstream_spec_path = REPO_ROOT / "packages/openai-contract/upstream/openapi.yaml"
    generated_spec_path = REPO_ROOT / "packages/openai-contract/generated/hive-openapi.yaml"
    support_matrix_markdown_path = REPO_ROOT / "docs/support-matrix.md"

    matrix_document, matrix_lookup = load_matrix(matrix_path)
    public_operations = render_openapi(
        upstream_spec_path,
        matrix_lookup,
        generated_spec_path,
    )
    render_markdown(matrix_document, support_matrix_markdown_path)

    print(f"Generated {public_operations} public operations")
    print(f"Wrote {generated_spec_path.relative_to(REPO_ROOT)}")
    print(f"Wrote {support_matrix_markdown_path.relative_to(REPO_ROOT)}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
