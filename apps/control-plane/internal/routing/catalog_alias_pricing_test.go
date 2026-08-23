package routing

import (
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The catalog restructure migration prices four new aliases against upstream
// provider list rates. Nothing in the running system re-derives those credit
// figures, so a typo in one of them is a silent mispricing that ships: the
// number looks plausible, every test stays green, and the first signal is a
// margin that does not add up weeks later. Issue #689 and issue #965 were both
// that shape.
//
// These tests close the gap that can be closed without a network call. CI holds
// no provider API keys, and a test that needs one is a test that skips, so the
// rates are pinned in a committed snapshot (testdata/provider_rates_2026-08-22.json)
// taken at the moment the migration was written. The snapshot's own header
// spells out what it cannot see: a rate the provider changes afterwards.
const (
	pricingMigrationRelPath = "supabase/migrations/20260822_02_catalog_alias_restructure.sql"
	providerRatesRelPath    = "testdata/provider_rates_2026-08-22.json"

	// creditsPerUSD mirrors apps/control-plane/internal/payments/types.go, and
	// marginNum/marginDen express the 1.4 margin exactly. Both are integers fed
	// to math/big so no float64 ever touches a money figure, per the repo rule.
	creditsPerUSD = 100000
	marginNum     = 14
	marginDen     = 10
)

// deriveRow is one "-- DERIVE|" line in the migration header. The migration
// documents its own arithmetic in a machine-readable form precisely so this
// test can check it rather than a reader having to.
type deriveRow struct {
	Alias         string
	RouteID       string
	ProviderModel string
	Field         string // "in", "out" or "cache_read"
	USD           string
	Credits       string
}

type providerRate struct {
	ProviderModel string  `json:"provider_model"`
	In            string  `json:"usd_in_per_million"`
	Out           string  `json:"usd_out_per_million"`
	CacheRead     *string `json:"usd_cache_read_per_million"`
}

type providerRatesFixture struct {
	FetchedUTC string         `json:"fetched_utc"`
	Models     []providerRate `json:"models"`
}

// expectedCredits computes ceil(usd * MARGIN * CREDITS_PER_USD) in exact
// rational arithmetic. big.Rat parses a decimal string without loss, so
// "0.01278" is 1278/100000 and not the nearest float64 to it, which matters:
// that particular rate is the one row in this migration whose product is not a
// whole number, so it is the row a float would round differently.
func expectedCredits(t *testing.T, usd string) *big.Int {
	t.Helper()

	rate, ok := new(big.Rat).SetString(usd)
	if !ok {
		t.Fatalf("provider rate %q is not a valid decimal", usd)
	}

	product := new(big.Rat).Mul(rate, big.NewRat(marginNum*creditsPerUSD, marginDen))

	quotient, remainder := new(big.Int).QuoRem(product.Num(), product.Denom(), new(big.Int))
	if remainder.Sign() != 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	return quotient
}

func readPricingMigration(t *testing.T) string {
	t.Helper()
	return readRepoFile(t, pricingMigrationRelPath)
}

func loadProviderRates(t *testing.T) map[string]providerRate {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(repoRoot(t), filepath.FromSlash("apps/control-plane/internal/routing"), filepath.FromSlash(providerRatesRelPath)))
	if err != nil {
		t.Fatalf("read provider rate snapshot: %v", err)
	}

	var fixture providerRatesFixture
	if err := json.Unmarshal(body, &fixture); err != nil {
		t.Fatalf("parse provider rate snapshot: %v", err)
	}
	if len(fixture.Models) == 0 {
		t.Fatal("provider rate snapshot is empty; it is the only offline record of what these prices were derived from")
	}

	byModel := make(map[string]providerRate, len(fixture.Models))
	for _, m := range fixture.Models {
		byModel[m.ProviderModel] = m
	}
	return byModel
}

var deriveLineRe = regexp.MustCompile(`(?m)^--\s*DERIVE\|([^\n]*)$`)

func parseDeriveRows(t *testing.T, migration string) []deriveRow {
	t.Helper()

	matches := deriveLineRe.FindAllStringSubmatch(migration, -1)
	if len(matches) == 0 {
		t.Fatalf("%s declares no '-- DERIVE|' rows; every price in this repo must show its derivation", pricingMigrationRelPath)
	}

	var rows []deriveRow
	for _, m := range matches {
		fields := strings.Split(m[1], "|")
		for i := range fields {
			fields[i] = strings.TrimSpace(fields[i])
		}
		// The header row labels the columns; skip it rather than parse it.
		if len(fields) > 0 && strings.EqualFold(fields[0], "alias_id") {
			continue
		}
		if len(fields) != 6 {
			t.Fatalf("malformed DERIVE row %q: want 6 pipe-separated fields (alias|route|provider_model|field|usd|credits), got %d", m[1], len(fields))
		}
		rows = append(rows, deriveRow{
			Alias:         fields[0],
			RouteID:       fields[1],
			ProviderModel: fields[2],
			Field:         fields[3],
			USD:           fields[4],
			Credits:       fields[5],
		})
	}
	if len(rows) == 0 {
		t.Fatal("DERIVE table has a header but no data rows")
	}
	return rows
}

// TestCatalogAliasPricesMatchProviderRates is the core guard: every credit
// figure the migration writes must equal ceil(rate * 1.4 * 100000) for the rate
// the snapshot records for that exact provider_model.
//
// It fails if a credit number is edited without its rate, if a rate is edited
// without its credit number, if the arithmetic is simply wrong, or if the
// migration names a provider_model that was never verified against a live
// provider catalog.
func TestCatalogAliasPricesMatchProviderRates(t *testing.T) {
	migration := readPricingMigration(t)
	rates := loadProviderRates(t)

	for _, row := range parseDeriveRows(t, migration) {
		rate, ok := rates[row.ProviderModel]
		if !ok {
			t.Errorf("alias %s routes to provider_model %q, which is absent from the verified provider snapshot; either the model id is wrong (a dropped tilde or a bad date suffix looks exactly like this) or the snapshot needs re-fetching",
				row.Alias, row.ProviderModel)
			continue
		}

		var snapshotUSD string
		switch row.Field {
		case "in":
			snapshotUSD = rate.In
		case "out":
			snapshotUSD = rate.Out
		case "cache_read":
			if rate.CacheRead == nil {
				t.Errorf("alias %s documents a cache_read price for %q, but the provider publishes no cache-read rate for that model",
					row.Alias, row.ProviderModel)
				continue
			}
			snapshotUSD = *rate.CacheRead
		default:
			t.Errorf("alias %s has unknown DERIVE field %q; want in, out or cache_read", row.Alias, row.Field)
			continue
		}

		if row.USD != snapshotUSD {
			t.Errorf("alias %s %s: migration documents the provider rate as $%s per million, snapshot recorded $%s",
				row.Alias, row.Field, row.USD, snapshotUSD)
			continue
		}

		want := expectedCredits(t, snapshotUSD)
		if want.String() != row.Credits {
			t.Errorf("alias %s %s: ceil($%s * 1.4 * 100000) = %s credits, migration claims %s",
				row.Alias, row.Field, snapshotUSD, want.String(), row.Credits)
		}
	}
}

// aliasInsertRe pulls the alias_ids out of the model_aliases INSERT so the test
// can prove the DERIVE table covers all of them. Without this, adding a fifth
// alias and forgetting to document its price would leave every other assertion
// green: the guard would silently stop guarding the new row.
var (
	aliasInsertBlockRe = regexp.MustCompile(`(?is)insert\s+into\s+public\.model_aliases\s*\((.*?)\)\s*values(.*?)on\s+conflict`)
	aliasIDRe          = regexp.MustCompile(`(?m)^\s*\(\s*'([a-z0-9][a-z0-9._-]*)'`)
)

// insertedAliases returns the alias_ids this migration INSERTS into
// model_aliases, as distinct from the ones it merely repoints or reprices with
// an UPDATE. Several guards below apply only to brand-new aliases: an existing
// alias already carries its policy row and its policy-group membership from an
// earlier migration, so demanding those here would be demanding a no-op.
func insertedAliases(t *testing.T, migration string) map[string]bool {
	t.Helper()

	block := aliasInsertBlockRe.FindStringSubmatch(migration)
	if block == nil {
		t.Fatalf("%s has no recognisable INSERT INTO public.model_aliases ... VALUES ... ON CONFLICT block", pricingMigrationRelPath)
	}

	matches := aliasIDRe.FindAllStringSubmatch(block[2], -1)
	if len(matches) == 0 {
		t.Fatal("parsed the model_aliases INSERT block but found no alias_id literals in it; the regex and the migration's formatting have diverged, so this guard is not actually reading the rows it claims to check")
	}

	out := make(map[string]bool, len(matches))
	for _, m := range matches {
		out[m[1]] = true
	}

	// A PARTIAL parse is the dangerous case, and the zero-match check above
	// does not catch it. aliasIDRe anchors each alias on its own line, so
	// reformatting one VALUES row onto a single line would drop that alias
	// from the set silently, and every guard built on this helper would then
	// check fewer rows than it claims while still reporting green. Counting
	// the INSERT's own opening parens gives an independent expectation to
	// compare against, so a partial parse fails as loudly as a total one.
	wantRows := strings.Count(block[2], "(\n")
	if wantRows > 0 && len(out) != wantRows {
		t.Fatalf("parsed %d alias ids from the model_aliases INSERT but the block opens %d value rows; the regex and the migration's formatting have diverged and this guard is silently checking a subset", len(out), wantRows)
	}
	return out
}

func TestEveryInsertedAliasDocumentsItsDerivation(t *testing.T) {
	migration := readPricingMigration(t)

	documented := make(map[string]bool)
	for _, row := range parseDeriveRows(t, migration) {
		documented[row.Alias] = true
	}

	for alias := range insertedAliases(t, migration) {
		if !documented[alias] {
			t.Errorf("alias %s is inserted into model_aliases but has no '-- DERIVE|' row documenting where its price came from", alias)
		}
	}
}

// retiredRoutes are the OpenRouter routes hive-default and hive-auto used to
// take, mapped to the Groq route each alias moves onto.
var retiredRoutes = map[string]struct{ alias, replacement string }{
	"route-openrouter-default": {"hive-default", "route-groq-default"},
	"route-openrouter-auto":    {"hive-auto", "route-groq-auto"},
}

// TestRetiredRoutesAreDisabledAndRepointed guards the three-part move that has
// to happen together for hive-default and hive-auto.
//
// The old route must be DISABLED rather than repointed in place. Repointing was
// the tempting one-line version and it is unsafe: litellmconfig's mergeParams
// deliberately preserves every litellm_params key the database does not own, so
// the OpenRouter-specific extra_body block those two entries carry in
// deploy/litellm/config.yaml would stay attached to a route now pointing at
// Groq, and would be sent to Groq on every request to the default model, with
// no sync able to remove it. Disabling the route id makes the merge drop the
// whole stale entry instead.
//
// The old route must also stop being named by its alias's policy, or the policy
// points at something SelectRoute filters out.
func TestRetiredRoutesAreDisabledAndRepointed(t *testing.T) {
	migration := readPricingMigration(t)

	disable := regexp.MustCompile(`(?is)update\s+public\.provider_routes\s+set\s+health_state\s*=\s*'disabled'[^;]*?;`)
	disableStmts := strings.Join(disable.FindAllString(migration, -1), "\n")
	if disableStmts == "" {
		t.Fatal("no UPDATE public.provider_routes ... SET health_state = 'disabled' statement found; the two OpenRouter routes must be retired, not left enabled alongside their replacements")
	}

	for old, move := range retiredRoutes {
		if !strings.Contains(disableStmts, "'"+old+"'") {
			t.Errorf("route %s is still enabled: it must be disabled when %s moves to %s, or the alias would have two enabled routes and stop being priceable",
				old, move.alias, move.replacement)
		}

		if !regexp.MustCompile(`(?is)update\s+public\.alias_route_policies\s+set\s+fallback_order\s*=\s*'\["`+
			regexp.QuoteMeta(move.replacement)+`"\]'::jsonb[^;]*?alias_id\s*=\s*'`+
			regexp.QuoteMeta(move.alias)+`'[^;]*?;`).MatchString(migration) {
			t.Errorf("%s still has no alias_route_policies update pointing fallback_order at [\"%s\"]; its policy would keep naming the retired route %s",
				move.alias, move.replacement, old)
		}

		// The stale entry must not be resurrected by an in-place repoint.
		if regexp.MustCompile(`(?is)update\s+public\.provider_routes\s+set\s+[^;]*?provider_model\s*=[^;]*?route_id\s*=\s*'` + regexp.QuoteMeta(old) + `'`).MatchString(migration) {
			t.Errorf("route %s is repointed in place; it must be disabled instead, or its OpenRouter extra_body block survives the config sync and is sent to Groq", old)
		}
	}
}

// TestDerivedCreditsAppearInTheMigrationBody catches the comment drifting away
// from the SQL. The DERIVE table could be internally perfect and still describe
// a price the INSERT does not actually write.
func TestDerivedCreditsAppearInTheMigrationBody(t *testing.T) {
	migration := readPricingMigration(t)

	// Strip the comment lines so the credit figures are searched for in real
	// SQL only. Otherwise the DERIVE table would satisfy this test by itself,
	// which is the classic assertion that cannot fail.
	var sqlOnly strings.Builder
	for _, line := range strings.Split(migration, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		sqlOnly.WriteString(line)
		sqlOnly.WriteString("\n")
	}
	body := sqlOnly.String()

	for _, row := range parseDeriveRows(t, migration) {
		if !regexp.MustCompile(`\b` + regexp.QuoteMeta(row.Credits) + `\b`).MatchString(body) {
			t.Errorf("alias %s %s: DERIVE table declares %s credits, but that figure appears nowhere in the migration's SQL",
				row.Alias, row.Field, row.Credits)
		}
	}
}

// TestHiveFastStaysInvocableAfterDeprecation guards the back-compat promise.
// hive-fast is the model id stored in the owner's existing Open WebUI
// conversations and in live API clients, so the restructure must not remove it,
// must not stop it resolving, and must not reprice it. Marking it deprecated is
// the only permitted change.
//
// visibility is the field that actually gates invocation: catalog.AliasVisibleToTenant
// entitles 'public' and 'preview' and fails closed on everything else, and the
// same predicate serves both the model listing and the inference-time check. So
// a migration that set hive-fast's visibility to 'internal' would silently break
// every saved chat, which is exactly what this asserts against.
func TestHiveFastStaysInvocableAfterDeprecation(t *testing.T) {
	migration := readPricingMigration(t)

	lower := strings.ToLower(migration)

	if strings.Contains(lower, "delete from public.model_aliases") {
		t.Error("the restructure must not DELETE from model_aliases: hive-fast is stored per-chat in existing Open WebUI conversations and would 404 for every one of them")
	}
	if strings.Contains(lower, "'hive-fast'") && regexp.MustCompile(`(?is)update\s+public\.model_aliases.*?set.*?visibility\s*=\s*'(internal|restricted)'.*?'hive-fast'`).MatchString(migration) {
		t.Error("hive-fast must keep visibility 'public': AliasVisibleToTenant fails closed on 'internal', which would block invocation, not merely hide the alias from the picker")
	}

	// The deprecation marker itself must be present and must be a value the
	// CHECK constraint allows. model_aliases_lifecycle_check permits only
	// 'stable', 'preview' and 'hidden' (20260331_01_model_catalog.sql), so a
	// literal 'deprecated' would abort the migration at apply time.
	if !regexp.MustCompile(`(?is)update\s+public\.model_aliases.*?lifecycle\s*=\s*'hidden'.*?'hive-fast'`).MatchString(migration) {
		t.Error("hive-fast must be marked with lifecycle = 'hidden'; that is the deprecation marker this restructure promised")
	}
	if regexp.MustCompile(`lifecycle\s*=\s*'deprecated'`).MatchString(migration) {
		t.Error("lifecycle = 'deprecated' violates model_aliases' CHECK constraint (allowed: stable, preview, hidden) and would abort the migration on apply")
	}
}

// TestOneAliasOneEnabledRoute enforces the owner's one-model-one-price rule at
// the level this migration can be checked offline: each new alias declares
// exactly one route, and its alias_route_policies fallback_order names that one
// route and nothing else. A second enabled route makes the alias's cost depend
// on which route won, which is not priceable at the alias level.
func TestOneAliasOneEnabledRoute(t *testing.T) {
	migration := readPricingMigration(t)
	inserted := insertedAliases(t, migration)

	routesByAlias := map[string]map[string]bool{}
	for _, row := range parseDeriveRows(t, migration) {
		if routesByAlias[row.Alias] == nil {
			routesByAlias[row.Alias] = map[string]bool{}
		}
		routesByAlias[row.Alias][row.RouteID] = true
	}

	for alias, routes := range routesByAlias {
		// The one-route invariant applies to every alias this migration
		// touches, repointed ones included: an alias whose cost depends on
		// which of two routes won is not priceable at the alias level.
		if len(routes) != 1 {
			t.Errorf("alias %s declares %d routes; the one-model-one-price rule allows exactly one enabled route per alias", alias, len(routes))
			continue
		}

		// The pinned-policy row is only demanded of aliases this migration
		// actually creates. A repointed alias keeps the alias_route_policies
		// row an earlier migration already gave it, and since the repoint does
		// not change its route_id, that row stays correct untouched.
		if !inserted[alias] {
			continue
		}

		var routeID string
		for r := range routes {
			routeID = r
		}

		wantPolicy := regexp.MustCompile(`\('` + regexp.QuoteMeta(alias) + `'\s*,\s*'[a-z]+'\s*,\s*false\s*,\s*'\["` + regexp.QuoteMeta(routeID) + `"\]'::jsonb\)`)
		if !wantPolicy.MatchString(migration) {
			t.Errorf("alias %s needs an alias_route_policies row whose fallback_order is exactly [\"%s\"]; found none", alias, routeID)
		}
	}
}

// soleCarrierFlags are capability flags held by exactly ONE route in the whole
// catalog before this migration, route-openrouter-auto, granted by
// 20260414_01_provider_capabilities_media_columns.sql and never granted to any
// other row since. supports_tts and supports_stt were on that list too until
// 20260717_02 correctly moved them to the voice routes.
var soleCarrierFlags = []string{
	"supports_batch",
	"supports_image_generation",
	"supports_image_edit",
}

// TestDisablingASoleCapabilityCarrierHandsItsFlagsOn is a regression guard for
// a real defect this migration shipped in an earlier revision and that no other
// test could see.
//
// Disabling route-openrouter-auto removes the only route carrying these three
// flags. SelectRoute skips disabled candidates and then hard filters on each
// flag, and both batchstore/submitter.go and batchstore/local_executor_adapters.go
// send NeedBatch = true for EVERY batch, so the effect is not scoped to
// hive-auto: /v1/batches, /v1/images/generations and /v1/images/edits each find
// zero eligible routes for every alias in the system. It fails closed, so it is
// silent, and the other guards here cannot see it because they only read this
// migration's own text for pricing.
//
// The rule this encodes: if you disable the sole carrier of a capability, some
// route in the same migration has to pick it up, or you have deleted a product
// surface as a side effect of a repricing.
func TestDisablingASoleCapabilityCarrierHandsItsFlagsOn(t *testing.T) {
	migration := readPricingMigration(t)

	disable := regexp.MustCompile(`(?is)update\s+public\.provider_routes\s+set\s+health_state\s*=\s*'disabled'[^;]*?;`)
	disabled := strings.Join(disable.FindAllString(migration, -1), "\n")
	if !strings.Contains(disabled, "'route-openrouter-auto'") {
		t.Skip("route-openrouter-auto is not disabled by this migration, so its capabilities are not at risk")
	}

	// FindAll, not Find. This migration contains MORE THAN ONE
	// provider_capabilities INSERT: one for the four brand-new routes, which
	// has no business carrying media flags, and a later one for the two
	// replacement routes, which must. An earlier version of this guard read
	// only the first block and so failed against a correct migration.
	blocks := regexp.MustCompile(`(?is)insert\s+into\s+public\.provider_capabilities\s*\((.*?)\)\s*values(.*?)on\s+conflict`).FindAllStringSubmatch(migration, -1)
	if len(blocks) == 0 {
		t.Fatal("route-openrouter-auto is disabled but this migration has no provider_capabilities INSERT to hand its flags to")
	}

	for _, flag := range soleCarrierFlags {
		granted := false
		for _, block := range blocks {
			columnIndex := -1
			for i, col := range strings.Split(block[1], ",") {
				if strings.TrimSpace(col) == flag {
					columnIndex = i
					break
				}
			}
			if columnIndex < 0 {
				continue
			}
			// Naming the column is not enough. Find a VALUES row that actually
			// puts true in that column's position, so a row of all-false
			// cannot satisfy this guard.
			for _, row := range regexp.MustCompile(`\(([^()]*)\)`).FindAllStringSubmatch(block[2], -1) {
				fields := strings.Split(row[1], ",")
				if columnIndex < len(fields) && strings.TrimSpace(fields[columnIndex]) == "true" {
					granted = true
					break
				}
			}
			if granted {
				break
			}
		}
		if !granted {
			t.Errorf("this migration disables route-openrouter-auto, the only route in the catalog carrying %s, and grants that flag to no replacement route. Every endpoint gated on it would find zero eligible routes for every alias.", flag)
		}
	}
}

// TestNewAliasesReachDefaultTierKeys is the inert-change guard. An alias can be
// inserted, priced, routed and wired into LiteLLM and still be invisible to
// every customer, because api_key_policies.allowed_group_names defaults to
// ["default"] and a key never sees an alias outside its groups. That gap has
// already been patched twice by hand (20260717_01 for hive-auto and 20260717_02
// for the voice aliases), which is twice too many to leave unguarded.
func TestNewAliasesReachDefaultTierKeys(t *testing.T) {
	migration := readPricingMigration(t)

	// Scoped to newly inserted aliases. hive-default and hive-auto are already
	// members from 20260331_03 and 20260717_01 respectively, so requiring a
	// membership row for them here would be requiring a no-op.
	for alias := range insertedAliases(t, migration) {
		want := regexp.MustCompile(`\(\s*'default'\s*,\s*'` + regexp.QuoteMeta(alias) + `'\s*\)`)
		if !want.MatchString(migration) {
			t.Errorf("alias %s is never added to the 'default' model policy group, so no default-tier API key can call it; the alias would ship inert", alias)
		}
	}
}
