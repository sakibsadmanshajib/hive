# Phase 19 OWUI E2E Fixtures

Binary fixtures (`policy.pdf`, `cat.jpg`) are intentionally **not** committed
to git. The OWUI E2E suite skips RAG/vision specs unless these files are
present locally and `HIVE_TEST_DB_URL` is set.

To populate locally:

```bash
# policy.pdf — synthetic 2-page PDF whose body contains the phrase
# "password rotation policy: 90 days" so spec 05 can match the anchor.
pandoc -o policy.pdf <(printf '# Hive Test Policy\n\nPassword rotation policy: 90 days.\n')

# cat.jpg — any public-domain cat image under 200 KB
curl -o cat.jpg https://upload.wikimedia.org/wikipedia/commons/thumb/3/3a/Cat03.jpg/200px-Cat03.jpg
```

For CI, both files are restored from the `phase-19-fixtures` GitHub Actions
artifact cache (uploaded by an ops-side seed job out of scope for Plan 04).
