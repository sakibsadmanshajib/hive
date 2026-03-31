---
status: passed
phase: 02-identity-account-foundation
source:
  - 02-01-SUMMARY.md
  - 02-02-SUMMARY.md
  - 02-03-SUMMARY.md
  - 02-04-SUMMARY.md
  - 02-05-SUMMARY.md
  - 02-06-SUMMARY.md
  - 02-07-SUMMARY.md
started: 2026-03-29T08:10:37Z
updated: 2026-03-30T05:19:56Z
---

## Current Test
<!-- OVERWRITE each test - shows where we are -->

number: 12
name: Optional Billing Settings
expected: |
  `/console/settings/billing` lets a verified user save partial billing or legal-entity data with the helper copy
  `Optional until checkout or invoicing.` Billing does not create a new dashboard reminder, and unverified users are
  redirected to `/console/settings/profile`.
result: passed via local Playwright harness on 2026-03-30

## Tests

### 1. Cold Start Smoke Test
expected: Stop any running Hive services, start the stack fresh, then open the app. The control-plane boots without startup or migration errors, `/health` returns `{"status":"ok"}`, the web console loads on port 3000, and the first authenticated page request returns live data.
result: passed

### 2. Sign In and Root Routing
expected: Visiting `/console` while signed out sends you to `/auth/sign-in`. After signing in, visiting `/` lands on `/console` rather than leaving you on an auth route.
result: passed

### 3. Session Persistence
expected: After signing in, refresh the page or revisit `/console`. The session persists and you stay in the console instead of being sent back to sign-in.
result: passed

### 4. Sign Up and Verification Return
expected: `/auth/sign-up` accepts an email and password, starts the verification flow, and the verification callback returns to Hive without allowing arbitrary redirect targets.
result: passed

### 5. Password Recovery and Reset
expected: `/auth/forgot-password` initiates a reset, the recovery link returns to Hive, and `/auth/reset-password` accepts a new password successfully.
result: passed

### 6. Limited Console for Unverified User
expected: With an unverified account, the console still loads, a verification banner is visible, and restricted actions like teammate invites stay disabled rather than failing invisibly.
result: passed

### 7. Profile Settings Reachable While Unverified
expected: With an unverified account, `/console/settings/profile` stays reachable and shows the email-maintenance controls, including the resend verification action.
result: passed

### 8. Workspace Switching
expected: If the user belongs to more than one workspace, changing the workspace switcher returns to `/console` and the displayed workspace context changes to the selected account.
result: passed

### 9. Invitation Acceptance Without Auto-Switch
expected: Accepting an invitation joins the new workspace successfully, but the current workspace does not auto-switch until the user changes it in the workspace switcher.
result: passed

### 10. Core Setup Reminder and Completion
expected: An incomplete account shows the minimal setup reminder on `/console`. Finishing `/console/setup` saves the core owner, account, and location fields and removes the reminder when you return to the dashboard.
result: passed

### 11. Core Profile Settings Save
expected: `/console/settings/profile` lets you edit the same core profile fields later, and the saved values are reflected when you revisit the page or dashboard.
result: passed

### 12. Optional Billing Settings
expected: `/console/settings/billing` lets a verified user save partial billing or legal-entity data with the helper copy `Optional until checkout or invoicing.` Billing does not create a new dashboard reminder, and unverified users are redirected to `/console/settings/profile`.
result: passed

## Summary

total: 12
passed: 12
issues: 0
pending: 0
skipped: 0

## Gaps

none
