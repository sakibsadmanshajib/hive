## Goal

Bring the `docs/` folder into alignment with the documented conventions by identifying and relocating out-of-place docs (especially plans vs designs vs audits) and updating indexes so maintainers can reliably find the current source of truth.

## Assumptions

- Existing index docs (`docs/README.md`, `docs/plans/README.md`, `docs/design/README.md`, `docs/runbooks/README.md`) describe the intended structure accurately and should be treated as the target state.
- Files under `docs/plans/completed/` and `docs/design/archive/` are already correctly placed and do not need structural changes, aside from index/reference updates if links move.
- Superpowers-specific artifacts under `docs/superpowers/` are allowed to diverge slightly from the main `docs/plans/` conventions but should still be discoverable from the main `docs/` index.
- No external tooling (build, tests) depends on specific doc file paths beyond human-facing links in markdown.

## Plan

1. **Inventory current docs layout**
   - **Files**: `docs/README.md`, `docs/plans/README.md`, `docs/design/README.md`, directory listings under `docs/`, `docs/plans/`, `docs/design/`, `docs/runbooks/`, `docs/audits/`, `docs/architecture/`, `docs/release/`, `docs/superpowers/`.
   - **Change**: No code changes; capture a written inventory of how docs are currently organized (per subfolder) and note obvious mismatches with the conventions described in the index files (e.g. design docs living under `docs/plans/` root, plans left in root that should be `completed/`).
   - **Verify**: Run `ls docs docs/plans docs/design docs/runbooks docs/audits docs/architecture docs/release docs/superpowers` to ensure the inventory covers all relevant subfolders.

2. **Propose target structure and classification rules**
   - **Files**: This plan file, `docs/README.md`, `docs/plans/README.md`, `docs/design/README.md`.
   - **Change**: Define simple, explicit rules for where each kind of doc should live (e.g. dated design docs vs implementation plans vs audits vs runbooks) and draft a mapping from each currently out-of-place file to its intended destination folder, without actually moving files yet.
   - **Verify**: Use `rg "Plans Index" docs/plans/README.md && rg "Design Docs Index" docs/design/README.md` to confirm that the proposed rules do not conflict with the existing documented conventions.

3. **Move out-of-place plan and design docs into the correct subfolders**
   - **Files**: Individual misplaced docs such as dated `*-design.md` files currently under `docs/plans/` root, any completed plans still in `docs/plans/` root that should live under `docs/plans/completed/`, and any design-like artifacts currently outside `docs/design/`.
   - **Change**: For each file identified in step 2, move it to the appropriate target folder (e.g. `docs/design/active/` or `docs/plans/completed/`) while preserving filenames and updating any obvious intra-repo links that reference the old path.
   - **Verify**: Run `rg "<moved-filename>" docs -n` for each moved file to ensure there are no stale references to the old location, and `ls` the target directories to confirm the files now appear where expected.

4. **Align index docs with the new structure**
   - **Files**: `docs/README.md`, `docs/plans/README.md`, `docs/design/README.md`, `docs/runbooks/README.md` (if needed), plus any other index-like docs that reference moved files.
   - **Change**: Update lists and examples in the index docs so they point at the new canonical locations and describe the refined placement rules (e.g. clarifying where dated design docs vs completed plans should live, how `docs/superpowers/` relates to `docs/plans/`).
   - **Verify**: Run `rg "2026-" docs/README.md docs/plans/README.md docs/design/README.md` to ensure all cited dated docs exist at the referenced paths, and open the files to visually confirm links render correctly in markdown.

5. **Add or refine a brief docs-structure conventions section**
   - **Files**: `docs/README.md` (and optionally `docs/plans/README.md` / `docs/design/README.md` for short clarifications).
   - **Change**: Add a concise “Docs structure conventions” subsection that summarizes where to put new plans, designs, runbooks, and audits, including any clarifications learned during this cleanup (e.g. how to treat `*-design.md` vs `*-implementation.md` docs, and how Superpowers-specific docs are organized).
   - **Verify**: Run `rg "Docs structure conventions" docs/README.md` to confirm the new section exists and read it to ensure it matches the actual folder layout.

6. **Lightweight validation and git hygiene**
   - **Files**: Entire repo (for verification and status checks only).
   - **Change**: No functional code changes expected; ensure that only documentation files were modified and that git history clearly reflects the structural cleanup.
   - **Verify**: Run `git status` to confirm only intended docs moved/edited, and (optionally) `pnpm lint` to ensure no tooling is unexpectedly sensitive to doc path changes.

## Risks & mitigations

- **Risk**: Moving docs breaks existing links in other markdown files or external references (e.g. READMEs, issues).
  - **Mitigation**: Use `rg` to search for each moved filename across `docs/` and the repo root and update all internal links; for external links (e.g. in GitHub issues), add a short note in the updated index docs describing the new locations.
- **Risk**: Misclassifying a doc as “completed” or “design” when it is still active or implementation-focused.
  - **Mitigation**: Prefer conservative moves: if a doc appears potentially active, keep it under `docs/plans/` root or `docs/design/active/` and document any uncertainties in this plan file or in a short “needs review” note.
- **Risk**: Superpowers-specific docs diverge from the main docs conventions and become hard to discover.
  - **Mitigation**: Ensure `docs/README.md` has a short pointer to `docs/superpowers/` and explain how Superpowers planning docs relate to the main `docs/plans/` hierarchy.

## Rollback plan

- Because this work is limited to moving and editing markdown files, rollback is straightforward:
  - Use `git status` to review the set of changed/moved docs.
  - If the restructuring causes confusion, run `git restore` (or `git checkout` on older Git versions) on specific files or directories (e.g. `docs/plans/`, `docs/design/`, `docs/README.md`) to revert them to their previous state.
  - If changes have already been committed, use `git revert <commit>` to undo the structural cleanup in a new commit while preserving history.
