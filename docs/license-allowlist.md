# Licence Exception Allowlist

## Purpose

The Hive sovereign edge is distributed as a proprietary, closed-integrated product to regulated buyers. AGPL and GPL dependencies are blocked by default because they impose source-disclosure obligations incompatible with that commercial model.

This document describes the process for adding a justified exception when a blocked dependency cannot be replaced.

## Blocked licences

The following SPDX identifiers fail the licence gate unconditionally:

| SPDX ID | Reason |
|---------|--------|
| GPL-1.0 | Source-disclosure obligation |
| GPL-2.0, GPL-2.0-only, GPL-2.0-or-later | Source-disclosure obligation |
| GPL-3.0, GPL-3.0-only, GPL-3.0-or-later | Source-disclosure obligation |
| AGPL-2.0 | Network-use source-disclosure obligation |
| AGPL-3.0, AGPL-3.0-only, AGPL-3.0-or-later | Network-use source-disclosure obligation |

## Allowed licences (no action required)

MIT, Apache-2.0, BSD-2-Clause, BSD-3-Clause, ISC, MPL-2.0, LGPL-2.1, LGPL-3.0, CC0-1.0, Unlicense, and other permissive or weak-copyleft licences that do not require source disclosure of the combined work.

## Adding an exception

### When is an exception appropriate?

An exception is appropriate only when all three conditions are met:

1. No permissively licensed alternative exists that provides equivalent functionality.
2. Legal counsel (or the responsible owner) has confirmed the specific usage does not trigger the licence's source-disclosure requirement (e.g. LGPL used only as a dynamically linked library with no modifications).
3. The justification is documented and reviewable.

### Process

1. Open a PR that modifies `scripts/license-allowlist.json` only (keep the allowlist change isolated from feature work).

2. Add an entry to the `packages` array:

   ```json
   {
     "name": "<exact package name as it appears in the SBOM>",
     "spdxId": "<blocked SPDX identifier>",
     "ecosystem": "go | npm",
     "justification": "One or two sentences explaining why replacement is not feasible and why the usage does not trigger source-disclosure obligations.",
     "approvedBy": "<GitHub username of approving owner or counsel>",
     "approvedAt": "<ISO 8601 date, e.g. 2026-07-01>"
   }
   ```

3. Request review from the project owner (sakibsadmanshajib) and, for AGPL packages, from legal counsel before merging.

4. The PR description must quote the relevant licence clause and explain why it does not apply to this usage.

### Example entry

```json
{
  "name": "some-library@2.1.0",
  "spdxId": "LGPL-2.1",
  "ecosystem": "npm",
  "justification": "Used only as a dynamically linked runtime dependency with no source modifications. LGPL-2.1 Section 6 permits distribution without source disclosure under these conditions.",
  "approvedBy": "sakibsadmanshajib",
  "approvedAt": "2026-07-01"
}
```

## Allowlist file location

`scripts/license-allowlist.json`

## Running the gate locally

```bash
# Full check (fails on violations):
./scripts/sbom-license-check.sh

# Audit mode (reports violations, exits 0):
./scripts/sbom-license-check.sh --report-only
```

Generated SBOM files (`SBOM.json`, `SBOM-go.json`, `SBOM-npm.json`) are written to the repo root and should not be committed to version control (they are gitignored). CI uploads them as build artefacts on every run and attaches them to release tags.
