# Console money and credential operations, proof captures

Taken 2026-08-06 against a locally running stack built from this branch:
`control-plane` and `web-console` from `deploy/docker`, pointed at the shared
Supabase project. Signed in through the real sign-in form as the E2E fixture
identity, which holds the `owner` role on the workspace under test.

Each capture carries an overlay naming the origin, the path and the capture
time, because a headless screenshot has no address bar and every console page
looks the same otherwise.

No credential is present in the pixels or in the text. The created API key
secret is masked in `03` before the capture was taken, and every key created
during this run was revoked immediately afterwards, which `04` shows.

| File | Shows |
| --- | --- |
| `01-budget-hard-cap-saved.png` | `PUT /api/budget/{id}` succeeds. Soft cap 9000.00 and hard cap 18000.00 saved, with the "Budget saved." confirmation. Previously this returned 404 and no cap could be stored. |
| `02-spend-alert-created.png` | `POST /api/spend-alerts/{id}` succeeds. A 100 percent alert with a delivery email is created, "Alert created." confirmation, and the alert appears in the active alerts table. Previously this returned 404. |
| `03-api-key-created.png` | `POST /api/v1/accounts/current/api-keys` succeeds. The key is issued and listed as Active. Previously this path had no route handler in the console app at all and returned Next's 404 HTML page. |
| `04-api-key-revoked.png` | Revoke succeeds and survives a page reload: every key in the table reads Revoked. This is the security-relevant half, since a leaked key could not be cut off through the product before this fix. |

Backend half of the same run, taken with a real session bearer against the
control-plane on `localhost:8081`:

```
PUT  /api/v1/budgets/{ws}          -> 200
POST /api/v1/spend-alerts/{ws}     -> 201
PUT  /api/v1/unmounted-surface/x   -> 404   (control: an unmounted path still 404s)
```

The control line matters. It shows the router change mounts two specific
prefixes rather than turning the `/api/v1/` surface into a catch-all.
