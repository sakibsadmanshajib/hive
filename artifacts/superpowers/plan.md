## Goal
Create a CHANGELOG.md file to track notable changes and update project documentation to reference it, ensuring compliance with AGENTS.md workflow.

## Assumptions
- The analysis of git history and existing docs is accurate.
- I have write access to the repository.
- The 'CHANGELOG.md' content should reflect the 'Unreleased' and '0.1.0' states.

## Plan
1.  **Create CHANGELOG.md**
    -   **File:** 'CHANGELOG.md'
    -   **Change:** Create file with 'Unreleased' and '0.1.0' sections.
    -   **Verify:** 'cat CHANGELOG.md' checks for content.

2.  **Update AGENTS.md**
    -   **File:** 'AGENTS.md'
    -   **Change:** Add 'Update CHANGELOG.md for all notable changes' to 'Documentation Discipline' section.
    -   **Verify:** 'grep CHANGELOG.md AGENTS.md' confirms addition.

3.  **Update README.md**
    -   **File:** 'README.md'
    -   **Change:** Add link to 'CHANGELOG.md' in 'Start Here' section.
    -   **Verify:** 'grep CHANGELOG.md README.md' confirms addition.

4.  **Update docs/README.md**
    -   **File:** 'docs/README.md'
    -   **Change:** Add link to '../../CHANGELOG.md' in 'Start Here' section.
    -   **Verify:** 'grep CHANGELOG.md docs/README.md' confirms addition.

## Risks & mitigations
-   **Risk:** Incorrect file paths or broken links.
    -   **Mitigation:** Verify file existence and relative paths.
-   **Risk:** Overwriting existing uncommitted changes (unlikely in fresh worktree).
    -   **Mitigation:** 'git status' check before starting.

## Rollback plan
-   'rm CHANGELOG.md'
-   'git checkout AGENTS.md README.md docs/README.md'
