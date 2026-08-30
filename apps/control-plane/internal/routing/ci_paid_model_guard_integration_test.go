//go:build integration

package routing

// The standing guard behind the owner's 2026-08-30 directive: no CI pipeline
// may ever call a PAID completion model. Free aliases only for anything that
// runs an LLM completion. Embedding models are allowed to be paid.
//
// WHY THIS IS NOT A LIST OF FORBIDDEN NAMES
//
// A denylist of alias names ("never say deepseek-v4-pro") goes stale the day
// somebody seeds a new paid alias, and nothing fails while it does. This guard
// runs the other way round. It reads the alias catalog out of the database the
// migration chain actually produced, decides from that data which aliases are
// upstream-free, and then requires every completion model value in CI to be
// one of them. An alias nobody has taught this guard about is PAID by default,
// so a new paid alias is caught on the first run that references it, with no
// edit here at all. The rule fails closed.
//
// WHAT "UPSTREAM-FREE" MEANS, AND THE ONE CONSTANT IT NEEDS
//
// An alias is upstream-free when every route it can still serve from (health
// state neither disabled nor eol) dispatches an upstream that costs Hive
// nothing:
//
//   - provider_model ends in ':free', OpenRouter's zero-priced variant
//     selector; or
//   - the route belongs to the free pool, the group of Hive-owned free
//     provider keys load balanced under one litellm_model_name
//     (supabase/migrations/20260824_02_free_pool_router.sql, decision D-048).
//
// The second arm needs the group's name as a constant, because a free-tier
// API key is free by virtue of the key, and no column records that. It is one
// constant, it names an allow condition rather than a deny condition, and if
// the pool were ever renamed the aliases resolving through it would stop
// matching and this guard would go RED rather than quietly passing. That is
// the safe direction for the one thing that cannot be derived.
//
// THE EMBEDDING EXEMPTION
//
// Keyed on capability data, never on a filename, a directory or a comment: an
// alias is exempt when its own provider_capabilities rows declare embeddings
// AND declare no completion surface. hive-embedding-default qualifies (and
// needs to: its healthy fallback route is a paid qwen embedding model). A chat
// alias cannot reach the exemption by being mentioned in a file that also
// mentions embeddings, and an alias declaring both embeddings and completions
// cannot buy a completion exemption with an embedding flag either.
//
// WHAT RUNS THIS
//
// The go-tests job in .github/workflows/ci.yml, control-plane leg, which is a
// required check on every pull request. That job stands up an ephemeral
// Postgres, applies supabase/migrations/*.sql in order and exports
// ROUTING_TEST_DB_URL, then runs this package with -tags integration. A
// missing DSN under CI is a wiring defect and fails rather than skips, for the
// reason issue #655 records.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

// freePoolGroupLitellmName is the shared litellm_model_name the free pool's
// members are emitted under. See the header for why this one string cannot be
// derived from the schema.
const freePoolGroupLitellmName = "route-free-pool"

// ciModelSurfaces are the file trees where a value can decide which model CI
// calls: the workflows themselves, the scripts they invoke, the SDK suites they
// run, and the compose files that supply those suites' defaults when a caller
// sets nothing.
var ciModelSurfaces = []struct {
	dir        string
	extensions []string
	recurse    bool
}{
	{dir: ".github/workflows", extensions: []string{".yml", ".yaml"}, recurse: true},
	{dir: "scripts", extensions: []string{".py", ".sh", ".mjs", ".js"}, recurse: true},
	{dir: "packages/sdk-tests", extensions: []string{".ts", ".py", ".mjs", ".js"}, recurse: true},
	{dir: "deploy/docker", extensions: []string{".yml", ".yaml"}, recurse: false},
}

// A binding only counts when its NAME ends in "model". That is what separates
// "this value is the model a request will carry" from the many places an alias
// id appears as prose, as a log-line fixture or as an argument to a metadata
// endpoint. `HIVE_TEST_MODEL`, `HIVE_TOOLS_MODEL`, `RAG_CHAT_MODEL`, a shell
// `model=` and a JSON `"model":` key all qualify; `CHAT_ALIASES`, a docstring's
// `alias="..."` and `client.models.retrieve(...)` do not.
var modelBindingName = regexp.MustCompile(`(?i)model$`)

// The binding shapes. The first is anchored at the start of a line, which is
// what keeps a commented-out or narrated binding (every comment line in these
// files begins with # or //) out of the results without needing a per-language
// comment stripper. The rest are inline forms that carry their own defaults.
var bindingPatterns = []*regexp.Regexp{
	// YAML mapping, shell assignment, `export NAME=`, compose environment entry.
	regexp.MustCompile(`(?m)^[ \t-]*(?:export[ \t]+)?([A-Za-z_][A-Za-z0-9_]*)[ \t]*[:=][ \t]*(\S.*)$`),
	// Python: os.getenv("NAME", "default") / os.environ.get("NAME", "default").
	regexp.MustCompile(`(?:getenv|environ\.get)\(\s*["']([A-Za-z0-9_]+)["']\s*,\s*(["'][^"']*["'])`),
	// JavaScript and TypeScript: process.env.NAME ?? "default" (or || "default").
	regexp.MustCompile(`process\.env\.([A-Za-z0-9_]+)\s*(?:\?\?|\|\|)\s*(["'][^"']*["'])`),
	// A `model` field in a request body literal, in any of these languages.
	// RE2 has no backreferences, so the quoting around the key is matched
	// loosely rather than paired; a mismatched pair is harmless here.
	regexp.MustCompile(`["']?\b(model)["']?\s*:\s*(["'][^"']*["'])`),
}

// ciModelBinding is one place in the repository where CI chooses a model.
type ciModelBinding struct {
	file  string
	line  int
	name  string
	alias string
}

// aliasFacts is what the catalog says about one alias, folded across every
// route it can still serve from.
type aliasFacts struct {
	aliasID      string
	pricingMode  string
	completion   bool
	embedding    bool
	upstreamFree bool
	capabilities map[string]bool
}

// paidCompletionException records a model value on a CI surface that resolves
// to a paid completion alias and is deliberately left in place. Every entry is
// a finding reported to the owner, never a quiet allowance.
//
// Two shapes, and the difference matters:
//
//   - capability set: the catalog holds no upstream-free alias that can do the
//     job. TestNoFlaggedExceptionHasBecomeSubstitutable re-derives that claim
//     from the catalog on every run and fails the moment a free alias acquires
//     the capability, so the entry cannot outlive its own justification.
//   - capability empty: the value does not configure a CI call at all, it
//     configures the deployed product. Repointing it is a product decision the
//     owner owns, not a CI spend decision. These carry `surfaces` so the
//     allowance is pinned to the exact file and cannot spread.
//
// surfaces, when non-empty, limits an entry to those repository paths.
type paidCompletionException struct {
	alias      string
	capability string
	surfaces   []string
	reason     string
}

var paidCompletionExceptions = []paidCompletionException{
	{
		alias:      "hive-auto",
		capability: "supports_image_generation",
		reason: "The image suite (packages/sdk-tests/js/tests/images/images.test.ts) has to send a " +
			"model that reaches SelectRoute for a NeedImageGeneration request, and hive-auto is the " +
			"only alias in the catalog declaring supports_image_generation at all. No upstream-free " +
			"alias declares it, so there is nothing to repoint at. Flagged for the owner rather than " +
			"downgraded: the alternative is deleting the only coverage of the image endpoint.",
	},
	{
		alias:    "hive-default",
		surfaces: []string{".github/workflows/deploy-demo-box.yml"},
		reason: "Not a CI call. This value is passed to scripts/install-agent-engine-host.sh and " +
			"becomes the model the DEPLOYED demo box's Cowork agent runs on, so moving it changes " +
			"the product a customer sees rather than what a pipeline spends. Repointing it at an " +
			"upstream-free alias is an owner decision about agent quality, and it is flagged here " +
			"rather than taken. The two workflows that genuinely run an agent inside CI " +
			"(agent-visual-proof.yml, agent-stream-delta-proof.yml) were repointed and are not " +
			"covered by this entry.",
	},
}

// appliesTo reports whether this exception covers a binding found at relPath.
func (e paidCompletionException) appliesTo(relPath string) bool {
	if len(e.surfaces) == 0 {
		return true
	}
	for _, surface := range e.surfaces {
		if surface == relPath {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Catalog side: read the aliases out of the database the migrations produced.
// ---------------------------------------------------------------------------

// capabilityColumns are read per alias so an exception can name the one it
// depends on and be re-checked against the live catalog.
var capabilityColumns = []string{
	"supports_chat_completions",
	"supports_responses",
	"supports_completions",
	"supports_embeddings",
	"supports_streaming",
	"supports_reasoning",
	"tools_supported",
	"supports_batch",
	"supports_image_generation",
	"supports_image_edit",
}

func loadAliasFacts(t *testing.T) map[string]aliasFacts {
	t.Helper()

	pool := connectCatalogDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var caps strings.Builder
	for _, col := range capabilityColumns {
		fmt.Fprintf(&caps, ", coalesce(bool_or(c.%s), false)", col)
	}

	// health_state is the enabled test used everywhere else in this package:
	// 'disabled' retires a route and 'eol' is the nvidia embedding route's
	// terminal state. A route in either state cannot be selected, so it must
	// not drag an alias's classification either way.
	query := `
		select a.alias_id,
		       coalesce(a.pricing_mode, 'fixed'),
		       coalesce(bool_and(
		           r.provider_model like '%:free'
		           or r.litellm_model_name = $1
		       ), false) as upstream_free,
		       count(r.route_id) as enabled_routes` + caps.String() + `
		  from public.model_aliases a
		  left join public.provider_routes r
		         on r.alias_id = a.alias_id
		        and r.health_state not in ('disabled', 'eol')
		  left join public.provider_capabilities c
		         on c.route_id = r.route_id
		 group by a.alias_id, a.pricing_mode`

	rows, err := pool.Query(ctx, query, freePoolGroupLitellmName)
	if err != nil {
		t.Fatalf("query alias catalog: %v", err)
	}
	defer rows.Close()

	facts := map[string]aliasFacts{}
	for rows.Next() {
		var (
			aliasID      string
			pricingMode  string
			upstreamFree bool
			enabled      int64
			flags        = make([]bool, len(capabilityColumns))
		)
		dest := []any{&aliasID, &pricingMode, &upstreamFree, &enabled}
		for i := range flags {
			dest = append(dest, &flags[i])
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatalf("scan alias catalog row: %v", err)
		}

		f := aliasFacts{
			aliasID:      aliasID,
			pricingMode:  pricingMode,
			upstreamFree: enabled > 0 && upstreamFree,
			capabilities: map[string]bool{},
		}
		for i, col := range capabilityColumns {
			f.capabilities[col] = flags[i]
		}
		f.completion = f.capabilities["supports_chat_completions"] ||
			f.capabilities["supports_responses"] ||
			f.capabilities["supports_completions"]
		f.embedding = f.capabilities["supports_embeddings"]
		facts[aliasID] = f
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate alias catalog: %v", err)
	}
	if len(facts) == 0 {
		t.Fatal("the alias catalog is empty; this guard cannot classify anything and must not pass")
	}
	return facts
}

// ---------------------------------------------------------------------------
// Repository side: find every place CI names a model.
// ---------------------------------------------------------------------------

func scanCIModelBindings(t *testing.T, aliases map[string]aliasFacts) []ciModelBinding {
	t.Helper()

	root := repoRoot(t)
	var found []ciModelBinding

	for _, surface := range ciModelSurfaces {
		base := filepath.Join(root, filepath.FromSlash(surface.dir))
		err := filepath.WalkDir(base, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if path != base && !surface.recurse {
					return filepath.SkipDir
				}
				if entry.Name() == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			if !hasExtension(entry.Name(), surface.extensions) {
				return nil
			}
			body, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				rel = path
			}
			found = append(found, bindingsIn(filepath.ToSlash(rel), string(body), aliases)...)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", surface.dir, err)
		}
	}

	sort.Slice(found, func(i, j int) bool {
		if found[i].file != found[j].file {
			return found[i].file < found[j].file
		}
		return found[i].line < found[j].line
	})
	return found
}

func hasExtension(name string, extensions []string) bool {
	for _, ext := range extensions {
		if strings.HasSuffix(name, ext) {
			return true
		}
	}
	return false
}

func bindingsIn(relPath, body string, aliases map[string]aliasFacts) []ciModelBinding {
	var out []ciModelBinding
	seen := map[string]bool{}

	for _, pattern := range bindingPatterns {
		for _, match := range pattern.FindAllStringSubmatchIndex(body, -1) {
			// The name is the LAST-but-one capture group and the value the last
			// one in every pattern above; that keeps the shapes readable while
			// letting the JSON form carry a quote group in front.
			n := len(match)/2 - 1
			name := groupText(body, match, n-1)
			value := groupText(body, match, n)
			if !modelBindingName.MatchString(strings.TrimSpace(name)) {
				continue
			}
			for _, alias := range aliasTokensIn(value, aliases) {
				key := fmt.Sprintf("%d|%s|%s", match[0], name, alias)
				if seen[key] {
					continue
				}
				seen[key] = true
				out = append(out, ciModelBinding{
					file:  relPath,
					line:  1 + strings.Count(body[:match[0]], "\n"),
					name:  strings.TrimSpace(name),
					alias: alias,
				})
			}
		}
	}
	return out
}

func groupText(body string, match []int, group int) string {
	start, end := match[2*group], match[2*group+1]
	if start < 0 || end < 0 {
		return ""
	}
	return body[start:end]
}

// tokenRe pulls every candidate model literal out of a right-hand side,
// whatever wraps it: bare, single or double quoted, inside a `${VAR:-default}`
// shell default, or inside a `${{ vars.X || 'default' }}` GitHub expression.
//
// A colon is deliberately NOT a token character. No alias id contains one, and
// including it swallowed the whole of `${HIVE_TEST_MODEL:-hive-default}` as a
// single token, which matched nothing and hid four paid compose defaults.
var tokenRe = regexp.MustCompile(`[A-Za-z0-9][A-Za-z0-9_./~-]*`)

// aliasTokensIn returns every catalog alias id named anywhere in a right-hand
// side. A provider prefix (`openai/hive-default`, which is how the agent engine
// addresses the gateway) resolves to the alias after the last slash.
func aliasTokensIn(value string, aliases map[string]aliasFacts) []string {
	var out []string
	for _, token := range tokenRe.FindAllString(value, -1) {
		candidates := []string{token}
		if i := strings.LastIndex(token, "/"); i >= 0 && i+1 < len(token) {
			candidates = append(candidates, token[i+1:])
		}
		for _, candidate := range candidates {
			if _, ok := aliases[candidate]; ok {
				out = append(out, candidate)
				break
			}
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// The guards themselves.
// ---------------------------------------------------------------------------

// TestNoCISurfaceCallsAPaidCompletionModel is the directive. Every model value
// on a CI surface must resolve to an upstream-free completion alias, to an
// embedding alias, or to a declared exception.
func TestNoCISurfaceCallsAPaidCompletionModel(t *testing.T) {
	aliases := loadAliasFacts(t)
	bindings := scanCIModelBindings(t, aliases)

	if len(bindings) == 0 {
		t.Fatal("scanned every CI surface and found no model binding at all; the scanner is broken, and a broken scanner passes this guard for free")
	}

	findException := func(alias, relPath string) (paidCompletionException, bool) {
		for _, e := range paidCompletionExceptions {
			if e.alias == alias && e.appliesTo(relPath) {
				return e, true
			}
		}
		return paidCompletionException{}, false
	}

	for _, b := range bindings {
		facts := aliases[b.alias]

		switch {
		case facts.embedding && !facts.completion:
			// The explicit, narrow exemption, keyed on the alias declaring
			// embeddings in its own provider_capabilities rows and nowhere
			// else. The directive permits paid embedding models.
			//
			// The `!facts.completion` half is load-bearing, not belt and
			// braces: an alias declaring BOTH embeddings and chat completions
			// would otherwise buy a completion exemption with an embedding
			// flag. Such an alias falls through to the upstream-free check
			// below, which is the correct treatment for something that can
			// serve a paid completion.
			t.Logf("embedding exemption: %s:%d %s = %s (paid embeddings are permitted by the directive)", b.file, b.line, b.name, b.alias)
		case !facts.completion:
			// Voice and any other non-completion alias. The directive is about
			// completion models; a transcription or speech alias bills a
			// different meter entirely and has no free counterpart in the
			// catalog. Reported, not enforced.
			t.Logf("out of scope (not a completion alias): %s:%d %s = %s", b.file, b.line, b.name, b.alias)
		case facts.upstreamFree:
			// The intended state. Logged rather than passed over in silence so
			// that a run of this test IS the audit: every model value CI can
			// choose, and its verdict, in one place a reviewer can read.
			t.Logf("ok (upstream-free): %s:%d %s = %s", b.file, b.line, b.name, b.alias)
		default:
			exception, ok := findException(b.alias, b.file)
			if !ok {
				t.Errorf("%s:%d sets %s to %q, a PAID completion alias (pricing_mode %s). CI may only call upstream-free completion aliases. Repoint it at one, or add a paidCompletionExceptions entry naming the capability no free alias provides.",
					b.file, b.line, b.name, b.alias, facts.pricingMode)
				continue
			}
			t.Logf("flagged for the owner, deliberately not repointed: %s:%d %s = %s (%s)", b.file, b.line, b.name, b.alias, exception.reason)
		}
	}
}

// TestNoFlaggedExceptionHasBecomeSubstitutable stops an exception outliving its
// justification. An entry is only legitimate while NO upstream-free completion
// alias in the catalog carries the capability it claims is unsubstitutable.
func TestNoFlaggedExceptionHasBecomeSubstitutable(t *testing.T) {
	aliases := loadAliasFacts(t)

	for _, exception := range paidCompletionExceptions {
		facts, ok := aliases[exception.alias]
		if !ok {
			t.Errorf("paidCompletionExceptions names alias %q, which is not in the catalog; delete the stale entry", exception.alias)
			continue
		}
		if exception.capability == "" {
			// A surface-pinned product decision. There is no capability claim
			// to re-derive, so the only thing to check is that the pin is real:
			// an entry pinned to a file that no longer exists is a dead
			// allowance nobody would notice.
			if len(exception.surfaces) == 0 {
				t.Errorf("the exception for %s names neither a capability nor a surface, so it allows a paid completion alias everywhere with nothing re-checking it", exception.alias)
				continue
			}
			for _, surface := range exception.surfaces {
				if _, statErr := os.Stat(filepath.Join(repoRoot(t), filepath.FromSlash(surface))); statErr != nil {
					t.Errorf("the exception for %s is pinned to %s, which does not exist; delete the stale entry", exception.alias, surface)
				}
			}
			continue
		}
		if _, known := facts.capabilities[exception.capability]; !known {
			t.Errorf("exception for %s names capability %q, which this guard does not read; add it to capabilityColumns or fix the entry", exception.alias, exception.capability)
			continue
		}
		if !facts.capabilities[exception.capability] {
			t.Errorf("exception for %s claims it is the only alias carrying %s, but the catalog says %s does not carry it either; the entry is wrong", exception.alias, exception.capability, exception.alias)
			continue
		}

		for aliasID, candidate := range aliases {
			if aliasID == exception.alias || !candidate.upstreamFree || !candidate.completion {
				continue
			}
			if candidate.capabilities[exception.capability] {
				t.Errorf("%s is upstream-free and now carries %s, so %s no longer has to be called from CI; repoint the surface and delete the exception",
					aliasID, exception.capability, exception.alias)
			}
		}
	}
}

// TestUpstreamFreeCompletionAliasesExist is the scanner's and the classifier's
// own smoke test. If the free-pool constant, the ':free' suffix convention or
// the health-state filter ever stopped matching reality, every alias would
// classify as paid, the guard above would go red for the wrong reason, and
// somebody would "fix" it by widening the exceptions. Fail here first, with a
// message that says what actually broke.
func TestUpstreamFreeCompletionAliasesExist(t *testing.T) {
	aliases := loadAliasFacts(t)

	var free, freeWithTools []string
	for aliasID, facts := range aliases {
		if !facts.upstreamFree || !facts.completion {
			continue
		}
		free = append(free, aliasID)
		if facts.capabilities["tools_supported"] {
			freeWithTools = append(freeWithTools, aliasID)
		}
	}
	sort.Strings(free)
	sort.Strings(freeWithTools)

	if len(free) == 0 {
		t.Fatalf("no upstream-free completion alias exists in the catalog; either the free pool group name %q changed or every free route was disabled", freePoolGroupLitellmName)
	}
	t.Logf("upstream-free completion aliases: %s", strings.Join(free, ", "))
	if len(freeWithTools) == 0 {
		t.Error("no upstream-free completion alias declares tools_supported, so the tool and structured-output suites have nowhere free to run; that is an owner decision, not a silent downgrade")
	} else {
		t.Logf("upstream-free and tools-capable: %s", strings.Join(freeWithTools, ", "))
	}
}
