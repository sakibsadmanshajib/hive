# GitHub Project Board Setup Guide

**Project:** Hive Roadmap (number 3)
**URL:** https://github.com/users/sakibsadmanshajib/projects/3

The GitHub Projects API does not support creating views programmatically. The three views below must be created once via the UI. Each procedure is under 6 clicks.

---

## View 1: All Items (Table)

**Purpose:** Full inventory across all horizons and sprints. Default reference view.

**Steps:**

1. Open https://github.com/users/sakibsadmanshajib/projects/3
2. Click **"New view"** (the "+" tab at the top of the view bar).
3. Select **"Table"** layout. Name it `All Items`.
4. Click the **"+" column header** (rightmost column) to add fields. Add in order: `Status`, `Horizon`, `Target`, `Labels`, `Milestone`. (Title is already present.)
5. Click **"Save"** (the button that appears in the view bar when there are unsaved changes).

Result: a flat table showing all 43 items with Status, Horizon, Target, Labels, and Milestone columns visible.

---

## View 2: Sprint Board (Kanban)

**Purpose:** Active sprint work-in-progress. Grouped by Status, filtered to the current sprint.

**Steps:**

1. Click **"New view"** and select **"Board"** layout. Name it `Sprint Board`.
2. Click **"Group by"** in the view options bar and select `Status`. The seven status columns (Backlog, Ready, In Progress, In Review, Needs Testing, Done, Blocked) will appear automatically.
3. Click **"Filter"** in the view options bar. Add filter: `Sprint` is `Sprint 1`. (Update this filter at the start of each sprint by changing the value to `Sprint 2`, `Sprint 3`, etc.)
4. Click **"Save"**.

Result: a Kanban board showing only Sprint 1 items, arranged across the seven status columns.

---

## View 3: Release Roadmap (Roadmap layout)

**Purpose:** Timeline view of all items grouped and coloured by release horizon. Used for stakeholder communication and milestone planning.

**Steps:**

1. Click **"New view"** and select **"Roadmap"** layout. Name it `Release Roadmap`.
2. In the **"Date field"** selector (top of the roadmap), choose `Target`.
3. Click **"Group by"** and select `Horizon`. The four groups (v1.1, v1.2, v1.3, watch) will appear as swimlanes.
4. Click **"Color by"** (in the view options) and select `Horizon` to colour-code items by release.
5. Click **"Save"**.

Result: a roadmap timeline where each item bar ends at its Target date (2026-06-30 for v1.1, 2026-09-30 for v1.2, 2026-12-31 for v1.3), grouped and coloured by horizon.

---

## Field Reference

These fields are already configured on the project. No setup required.

| Field | Type | Options |
|-------|------|---------|
| Status | Single select | Backlog, Ready, In Progress, In Review, Needs Testing, Done, Blocked |
| Sprint | Single select | Sprint 1, Sprint 2, Sprint 3, Backlog |
| Horizon | Single select | v1.1, v1.2, v1.3, watch |
| Target | Date | Per-item date (v1.1: 2026-06-30, v1.2: 2026-09-30, v1.3: 2026-12-31) |

---

## Workflow Notes

**Moving items through the board:** Use drag-and-drop on the Sprint Board view or `gh project item-edit` from the CLI (field and option IDs in SPRINT-01.md).

**Sprint filter update:** At the start of each sprint, open the Sprint Board view, click the active filter chip, and change the sprint value. No other configuration changes are needed.

**Watch items:** The four `watch` horizon items have no Target date by design. They will not appear on the Release Roadmap timeline but will appear in the All Items table.

**Adding new items:** New GitHub issues are automatically added to the project if the repository is linked. Set Horizon, Target, and Sprint immediately after creation to keep all three views accurate.
