# Proof: the free-alias-only CI guard can actually fail

Captured 2026-08-30 on the branch `fix/ci-free-alias-only-guard`, against a
throwaway `pgvector/pgvector:pg17` container carrying `.github/ci/test-db-bootstrap.sql`
plus every file in `supabase/migrations/` applied in order, which is the same
schema the `go-tests` job in `.github/workflows/ci.yml` builds for the
control-plane leg. Tests were run inside the repository toolchain image with
`ROUTING_TEST_DB_URL` pointed at that container.

No credential appears anywhere in this file. The only network calls made during
this work were to OpenRouter's public chat completions endpoint on a `:free`
model, and every one of them reported `cost: 0`.

## Part 1. A reintroduced paid value turns the guard red, and reverting turns it green

```text
############################################################
# STEP 1. Reintroduce a paid completion alias in ci.yml
############################################################
1293:      HIVE_TOOLS_MODEL: ${{ vars.CI_LIVE_INTEGRATION_TOOLS_MODEL || 'deepseek-v4-flash' }}

############################################################
# STEP 2. Run the guard. Expected: RED
############################################################
    ci_paid_model_guard_integration_test.go:466: .github/workflows/ci.yml:1293 sets HIVE_TOOLS_MODEL to "deepseek-v4-flash", a PAID completion alias (pricing_mode fixed). CI may only call upstream-free completion aliases. Repoint it at one, or add a paidCompletionExceptions entry naming the capability no free alias provides.
--- FAIL: TestNoCISurfaceCallsAPaidCompletionModel (0.77s)
FAIL
FAIL	github.com/sakibsadmanshajib/hive/apps/control-plane/internal/routing	0.778s
FAIL

############################################################
# STEP 3. Revert the reintroduced value
############################################################
1293:      HIVE_TOOLS_MODEL: ${{ vars.CI_LIVE_INTEGRATION_TOOLS_MODEL || 'hive-small' }}

############################################################
# STEP 4. Run the guard again. Expected: GREEN
############################################################
--- PASS: TestNoCISurfaceCallsAPaidCompletionModel (0.83s)
ok  	github.com/sakibsadmanshajib/hive/apps/control-plane/internal/routing	0.840s
```

## Part 2. An alias invented after the guard was written is caught too

This is the property that separates the guard from a denylist of names. The
alias below did not exist when the guard was written, is named nowhere in it,
and the guard source is not touched between red and green. It is seeded into
the catalog, referenced from a CI surface, and caught on the first run.

```text
############################################################
# STEP 1. Seed a brand new PAID alias the guard has never seen
############################################################
seeded

############################################################
# STEP 2. Point a CI surface at it, then run the UNCHANGED guard
#         Expected: RED
############################################################
1293:      HIVE_TOOLS_MODEL: ${{ vars.CI_LIVE_INTEGRATION_TOOLS_MODEL || 'brand-new-paid-alias' }}
    ci_paid_model_guard_integration_test.go:466: .github/workflows/ci.yml:1293 sets HIVE_TOOLS_MODEL to "brand-new-paid-alias", a PAID completion alias (pricing_mode fixed). CI may only call upstream-free completion aliases. Repoint it at one, or add a paidCompletionExceptions entry naming the capability no free alias provides.
--- FAIL: TestNoCISurfaceCallsAPaidCompletionModel (0.70s)
FAIL
FAIL	github.com/sakibsadmanshajib/hive/apps/control-plane/internal/routing	0.712s
FAIL

############################################################
# STEP 3. Revert the surface and drop the invented alias
############################################################
1293:      HIVE_TOOLS_MODEL: ${{ vars.CI_LIVE_INTEGRATION_TOOLS_MODEL || 'hive-small' }}
--- PASS: TestNoCISurfaceCallsAPaidCompletionModel (0.74s)
ok  	github.com/sakibsadmanshajib/hive/apps/control-plane/internal/routing	0.751s
```

The SQL used in step 1 of part 2 inserted one `model_aliases` row
(`brand-new-paid-alias`, fixed pricing), one `provider_routes` row on a paid
upstream that is neither a `:free` variant nor a free pool member, and one
`provider_capabilities` row declaring chat completions and tools. Step 3
deleted all three.

## Part 3. The classifier's own report, from the same database

```text
--- PASS: TestUpstreamFreeCompletionAliasesExist
    upstream-free completion aliases: hive-fast, hive-free, hive-medium, hive-small
    upstream-free and tools-capable: hive-fast, hive-medium, hive-small
```

## Part 4. Live capability evidence for the new tools default

Every probe below went to OpenRouter's public endpoint against
`dots-studio/dots-3-note-preview:free`, the upstream `route-free-small` serves
and therefore what `hive-small` resolves to. Every response reported
`"cost": 0`.

```text
-- forced tool_choice {"type":"function"} --
finish_reason = tool_calls, one tool_calls entry, arguments {"city": "Dhaka"}

-- tool_choice required --
finish= tool_calls | tool_calls= 1 | content_type= NoneType | cost= 0 | cached= 0
-- tool_choice none --
finish= length     | tool_calls= 0 | content_type= str      | cost= 0 | cached= 0
-- tool_choice auto --
finish= tool_calls | tool_calls= 1 | content_type= str      | cost= 0 | cached= 0
-- multi-turn tool result round trip --
finish= stop       | tool_calls= 0 | content_type= str      | cost= 0 | cached= 0
-- sampling params temperature/top_p/seed --
finish= length     | tool_calls= 0 | content_type= NoneType | cost= 0 | cached= 0

-- message.content type under response_format, three runs each --
json_object run1: str |cost= 0 |content= '{"city": "Dhaka", "population": "22 million"}'
json_object run2: str |cost= 0 |content= '{"city": "Dhaka", "population": "22 million"}'
json_object run3: str |cost= 0 |content= '{"city": "Dhaka", "population": "22 million"}'
json_schema run1: str |cost= 0 |content= '{"city": "Dhaka", "population":  "22 million"}'
json_schema run2: str |cost= 0 |content= '{\n  "city": "Dhaka",\n  "population": "22 million"\n}'
json_schema run3: str |cost= 0 |content= '{\n  "city": "Dhaka",\n  "population": "22 million"\n}'
```

`content_type = str` on 6 of 6 structured-output runs is the specific contract
the previous default, `deepseek-v4-flash`, could not hold: its `-latest` router
returned `message.content` as a parsed JSON object, as null and as a string
across probes on 2026-08-23 (run 32665985618). The `NoneType` on the two probes
above is a reasoning burn against a deliberately tiny `max_tokens` of 100 and
300, not a dropped parameter; the suites use 256 and 512.

`prompt_tokens_details.cached_tokens` is present and numeric on every response,
which is what `usage-accounting.test.ts` asserts.

## Part 5. The runtime half of the guard, exercised

Added in review: a repository variable is not in any diff, so
`CI_LIVE_INTEGRATION_MODEL` or `CI_LIVE_INTEGRATION_TOOLS_MODEL` set to a paid
alias would move this job's spend with nothing in the repository changing and
no static check going red. The `Refuse to bill a paid completion alias` step in
`.github/workflows/ci.yml` closes that by resolving whatever the two variables
actually hold through the seeded catalog, using the same predicate the Go guard
uses.

The step body below was extracted verbatim from the workflow with a YAML parser
and executed against the same throwaway database, once per case.

```text
== HIVE_TEST_MODEL=hive-free HIVE_TOOLS_MODEL=hive-small
HIVE_TEST_MODEL = hive-free is upstream-free
HIVE_TOOLS_MODEL = hive-small is upstream-free
   exit=0

== HIVE_TEST_MODEL=hive-free HIVE_TOOLS_MODEL=deepseek-v4-flash
HIVE_TEST_MODEL = hive-free is upstream-free
::error::HIVE_TOOLS_MODEL resolves to 'deepseek-v4-flash', a PAID completion alias. ...
   exit=1

== HIVE_TEST_MODEL=hive-default HIVE_TOOLS_MODEL=hive-small
::error::HIVE_TEST_MODEL resolves to 'hive-default', a PAID completion alias. ...
HIVE_TOOLS_MODEL = hive-small is upstream-free
   exit=1

== HIVE_TEST_MODEL=hive-free HIVE_TOOLS_MODEL=deepseek-v4-pro
::error::HIVE_TOOLS_MODEL resolves to 'deepseek-v4-pro', a PAID completion alias. ...
   exit=1

== HIVE_TEST_MODEL=hive-free HIVE_TOOLS_MODEL=hive-auto
::error::HIVE_TOOLS_MODEL resolves to 'hive-auto', a PAID completion alias. ...
   exit=1

== HIVE_TEST_MODEL=hive-free HIVE_TOOLS_MODEL=not-a-real-alias
::error::HIVE_TOOLS_MODEL resolves to 'not-a-real-alias', which the seeded catalog has no enabled route for, so nothing here can say what it would bill
   exit=1

== HIVE_TEST_MODEL= HIVE_TOOLS_MODEL=hive-small
::error::HIVE_TEST_MODEL is empty, so this job would call whatever a suite defaults to rather than the alias it selected
HIVE_TOOLS_MODEL = hive-small is upstream-free
   exit=1
```

Note the `deepseek-v4-pro` case. The name check that used to sit in the
`Declare which provider account this run bills` step caught exactly that one
alias and nothing else; this catches it as one instance of a general rule, and
catches `hive-auto`, `hive-default` and an alias that does not exist as well.

## Part 6. The pool-split coupling, and the new provider-qualified rule

Added in the second review round. The independent reviewer found that pinning
the free pool's group name by equality would redden a required check on `main`
the moment PR #1556 splits the pool into `route-free-pool-tools` and
`route-free-pool-open`: `hive-free` would stop matching, classify as PAID, and
two individually correct changes would deadlock the branch between them.

Step A below is that failure, reproduced deliberately by restoring the equality
match. Note that the fixture alias in `TestSplitFreePoolStillResolvesAsFree`
sits in THREE groups, the two #1556 introduces plus an invented third, so the
test is about the prefix rule rather than about two strings.

```text
############################################################
# A. Restore the equality match the review said would deadlock
############################################################
321:		           or r.litellm_model_name = $1
--- PASS: TestNoCISurfaceCallsAPaidCompletionModel (0.74s)
    ci_paid_model_guard_integration_test.go:973: an alias whose routes sit in 3 free pool groups classified as PAID. The pool group match is pinned to one literal name again, and splitting the pool will redden this guard on main.
--- FAIL: TestSplitFreePoolStillResolvesAsFree (0.10s)
FAIL

############################################################
# B. Restore the prefix match
############################################################
--- PASS: TestSplitFreePoolStillResolvesAsFree (0.47s)
ok  	github.com/sakibsadmanshajib/hive/apps/control-plane/internal/routing	0.476s

############################################################
# C. Reintroduce a paid provider-qualified upstream id
############################################################
308:          OPENROUTER_DEFAULT_MODEL=openrouter/openai/gpt-4o-mini
    ci_paid_model_guard_integration_test.go:682: .github/workflows/owui-nightly.yml:308 sets OPENROUTER_DEFAULT_MODEL to "openrouter/openai/gpt-4o-mini", a provider-qualified upstream model id that bypasses the alias catalog entirely and bills whatever that provider charges. Point it at a free upstream, or add an allowedUpstreamModelIDs entry saying why it is safe.
--- FAIL: TestNoCISurfaceCallsAPaidCompletionModel (0.87s)
FAIL

############################################################
# D. Revert it
############################################################
308:          OPENROUTER_DEFAULT_MODEL=
--- PASS: TestNoCISurfaceCallsAPaidCompletionModel (0.91s)
ok  	github.com/sakibsadmanshajib/hive/apps/control-plane/internal/routing	0.942s
```

Step C is the second new rule. An unresolved value used to be a silent pass. A
provider-qualified upstream id now fails unless declared, because such an id
never touches the alias catalog: it is substituted into
`deploy/litellm/config.yaml` and dispatched straight at the provider, so no
price or free-ness claim in this database applies to it. A bare literal is still
only reported, since the gateway refuses a model it has no alias row for, and
several of them are deliberate negative-path fixtures.

What counts as provider-qualified is read from `custom_providers.litellm_prefix`
rather than from "contains a slash". The first draft used the slash and
immediately failed on six file paths assigned to variables whose names contain
`MODEL`, for example `VENDORED_MODELS =
"vendor/open-webui/backend/open_webui/models/chats.py"`. Asking the catalog which
first segments are providers answers that without a list of file-extension
exclusions that would go stale on the first new language.
