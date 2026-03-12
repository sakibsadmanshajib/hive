#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
LABELS_FILE="$ROOT_DIR/tools/github/labels.json"
MILESTONES_FILE="$ROOT_DIR/tools/github/milestones.json"

if ! command -v gh >/dev/null 2>&1; then
  echo "gh CLI is required" >&2
  exit 1
fi

if ! command -v python3 >/dev/null 2>&1; then
  echo "python3 is required" >&2
  exit 1
fi

if [[ ! -f "$LABELS_FILE" || ! -f "$MILESTONES_FILE" ]]; then
  echo "Expected metadata files were not found under tools/github" >&2
  exit 1
fi

python3 - "$LABELS_FILE" "$MILESTONES_FILE" <<'PY'
import json
import subprocess
import sys
from urllib.parse import quote

labels_path, milestones_path = sys.argv[1], sys.argv[2]

with open(labels_path, "r", encoding="utf-8") as fh:
    labels = json.load(fh)

with open(milestones_path, "r", encoding="utf-8") as fh:
    milestones = json.load(fh)


def gh_api(path: str, method: str = "GET", fields: dict[str, str] | None = None) -> str:
    cmd = ["gh", "api"]
    if method != "GET":
        cmd.extend(["--method", method])
    cmd.append(path)
    for key, value in (fields or {}).items():
        cmd.extend(["-f", f"{key}={value}"])
    return subprocess.check_output(cmd, text=True)


def gh_api_paginated(path: str) -> list[dict]:
    output = subprocess.check_output(["gh", "api", path, "--paginate"], text=True)
    pages = []
    decoder = json.JSONDecoder()
    content = output.strip()
    index = 0

    while index < len(content):
        while index < len(content) and content[index].isspace():
            index += 1
        if index >= len(content):
            break
        page, next_index = decoder.raw_decode(content, index)
        if isinstance(page, list):
            pages.extend(page)
        else:
            pages.append(page)
        index = next_index

    return pages


print(f"Syncing labels from {labels_path}")
for label in labels:
    name = label["name"]
    color = label["color"]
    description = label.get("description", "")
    path = f"repos/{{owner}}/{{repo}}/labels/{quote(name, safe='')}"

    try:
        current = json.loads(gh_api(path))
    except subprocess.CalledProcessError:
        print(f"Creating label: {name}")
        gh_api(
            "repos/{owner}/{repo}/labels",
            method="POST",
            fields={
                "name": name,
                "color": color,
                "description": description,
            },
        )
        continue

    current_color = (current.get("color") or "").lstrip("#").lower()
    target_color = color.lstrip("#").lower()
    if current_color == target_color and (current.get("description") or "") == description:
        print(f"Label up to date: {name}")
        continue

    print(f"Updating label: {name}")
    gh_api(
        path,
        method="PATCH",
        fields={
            "new_name": name,
            "color": color,
            "description": description,
        },
    )

print(f"Syncing milestones from {milestones_path}")
current_milestones = gh_api_paginated("repos/{owner}/{repo}/milestones?state=all&per_page=100")

for milestone in milestones:
    title = milestone["title"]
    description = milestone.get("description", "")
    state = milestone.get("state", "open")
    current = next((item for item in current_milestones if item["title"] == title), None)

    if current is None:
        print(f"Creating milestone: {title}")
        gh_api(
            "repos/{owner}/{repo}/milestones",
            method="POST",
            fields={
                "title": title,
                "description": description,
                "state": state,
            },
        )
        continue

    if (current.get("description") or "") == description and current.get("state") == state:
        print(f"Milestone up to date: {title}")
        continue

    print(f"Updating milestone: {title}")
    gh_api(
        f"repos/{{owner}}/{{repo}}/milestones/{current['number']}",
        method="PATCH",
        fields={
            "title": title,
            "description": description,
            "state": state,
        },
    )

print("GitHub metadata sync complete.")
PY
