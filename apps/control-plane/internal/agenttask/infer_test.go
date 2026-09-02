package agenttask_test

import (
	"strings"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/agenttask"
)

// The default is the whole safety argument of issue #1623, so it is pinned
// first and on its own. A request with no coding evidence in it must land on
// the knowledge-work pack, because that pack is the superset: it ships the
// three publishing skills, it is the only pack whose sessions can publish a
// deck, and its own manifest permits arbitrary build and test commands, so a
// coding request that falls through to it still runs. The reverse mistake
// costs a capability.
func TestInferPack_DefaultsToKnowledgeWorkWithoutCodingEvidence(t *testing.T) {
	for _, instructions := range []string{
		"",
		"   \n\t ",
		"Summarise the attached quarterly report into a five slide deck.",
		"Draft a one page memo comparing our three shortlisted vendors.",
		"Research the Bangladesh data localisation rules and write up what applies to us.",
		"Turn these meeting notes into a project brief with owners and dates.",
		"Read the contract and list every clause that mentions termination.",
		// The exact string the visual proof for issue #1623 submits in its
		// knowledge-work frame. Pinned here so the pack that capture shows is
		// the one this function is mechanically held to, in CI, rather than a
		// number a capture script decided on its own.
		"Write a one page brief on how prepaid credit billing works for a new team member.",
	} {
		if got := agenttask.InferPack(instructions); got != agenttask.PackKnowledgeWork {
			t.Errorf("InferPack(%q) = %q, want %q", instructions, got, agenttask.PackKnowledgeWork)
		}
	}
}

// Positive evidence, one strong hit each. Every string here is a request a
// person would plausibly type into the composer, not a keyword list, because
// the rule has to survive real phrasing rather than its own test fixtures.
func TestInferPack_PicksCodingOnPositiveEvidence(t *testing.T) {
	for _, instructions := range []string{
		"Refactor the billing module so the FX conversion is in one place.",
		"Find out why the test suite is red on the payments package.",
		"Read the codebase and tell me where rate limiting is enforced.",
		"Clone the repository and add a health check endpoint.",
		"The build fails to compile after the dependency bump, work out why.",
		"Run the linter over apps/edge-api and fix what it reports.",
		"npm install is failing on a peer dependency, sort it out.",
		"Here is the stack trace, find the bug it points at.",
		"Add a unit test covering the empty-input branch.",
		"Update main.go so the flag defaults to false.",
		"Fix the type error in ComposerCoworkRow.svelte.",
		"Work out what this does:\n```go\nfunc main() {}\n```",
		// The exact string the visual proof for issue #1623 submits in its
		// coding frame, pinned for the same reason as its knowledge-work twin
		// above.
		"Refactor the retry helper in server.go and run the test suite.",
	} {
		if got := agenttask.InferPack(instructions); got != agenttask.PackCoding {
			t.Errorf("InferPack(%q) = %q, want %q", instructions, got, agenttask.PackCoding)
		}
	}
}

// Case and surrounding punctuation must not decide a launch. A person who
// starts a sentence with "Refactor" and one who writes "refactor," mid
// sentence asked for the same thing.
func TestInferPack_IsCaseAndPunctuationInsensitive(t *testing.T) {
	for _, instructions := range []string{
		"REFACTOR THE BILLING MODULE.",
		"Please (refactor) the billing module.",
		"refactor: billing module",
		"Refactor?",
	} {
		if got := agenttask.InferPack(instructions); got != agenttask.PackCoding {
			t.Errorf("InferPack(%q) = %q, want %q", instructions, got, agenttask.PackCoding)
		}
	}
}

// The failure mode a substring match would ship. Each of these contains a
// coding term inside a longer ordinary word, and none of them is a coding
// request. Matching on word boundaries is the whole reason the rule can be
// explained to the person it just surprised.
func TestInferPack_DoesNotMatchInsideLongerWords(t *testing.T) {
	for _, instructions := range []string{
		"Summarise the Gitanjali translation notes.",       // git
		"Write a profile of the Compilers of the Talmud.",  // compile
		"Draft the agenda for the Lintel Group board day.", // lint
		"Explain the repossession process to a new hire.",  // repo
	} {
		if got := agenttask.InferPack(instructions); got != agenttask.PackKnowledgeWork {
			t.Errorf("InferPack(%q) = %q, want %q", instructions, got, agenttask.PackKnowledgeWork)
		}
	}
}

// A source filename is structural evidence rather than vocabulary, so it is
// pinned separately: the extension list is what makes "look at server.go"
// coding without the sentence carrying any coding word at all. Document
// extensions must not trip it, since naming a file is how almost every
// knowledge-work request starts.
func TestInferPack_ReadsSourceFilenamesButNotDocumentOnes(t *testing.T) {
	coding := []string{
		"Have a look at server.go and tell me what it does.",
		"Walk me through apps/edge-api/internal/agenttask/handler.go.",
		"Explain what index.ts is for.",
		"What does analyse.py actually compute?",
	}
	for _, instructions := range coding {
		if got := agenttask.InferPack(instructions); got != agenttask.PackCoding {
			t.Errorf("InferPack(%q) = %q, want %q", instructions, got, agenttask.PackCoding)
		}
	}

	knowledge := []string{
		"Have a look at proposal.pdf and tell me what it commits us to.",
		"Summarise notes.md into a memo.",
		"Turn budget.xlsx into a chart deck.",
		"Read the transcript in interview.docx.",
	}
	for _, instructions := range knowledge {
		if got := agenttask.InferPack(instructions); got != agenttask.PackKnowledgeWork {
			t.Errorf("InferPack(%q) = %q, want %q", instructions, got, agenttask.PackKnowledgeWork)
		}
	}
}

// The inference feeds a launch, so it is an input-parsing path and gets the
// treatment one deserves: it must always answer, it must always answer with a
// pack the CHECK constraint on public.agent_tasks accepts, and it must not
// take unbounded time over a hostile body. 64 KiB is edge-api's own read
// limit on this endpoint, so that is the largest instructions field that can
// reach it.
func TestInferPack_AlwaysReturnsAValidPack(t *testing.T) {
	for name, instructions := range map[string]string{
		"empty":              "",
		"only whitespace":    " \t\n\r ",
		"nul bytes":          "\x00\x00refactor\x00",
		"unicode":            "রিফ্যাক্টর করুন 🙂 emoji and Bengali",
		"combining marks":    "réfactor",
		"very long":          strings.Repeat("a", 64<<10),
		"very long no space": strings.Repeat("refactoring", 6000),
		"sql-ish":            "'; DROP TABLE agent_tasks; --",
		"html":               "<script>alert('refactor')</script>",
	} {
		got := agenttask.InferPack(instructions)
		if !got.Valid() {
			t.Errorf("InferPack(%s) returned %q, which the pack CHECK constraint would reject", name, got)
		}
	}
}
