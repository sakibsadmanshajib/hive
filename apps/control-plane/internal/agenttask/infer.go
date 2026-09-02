package agenttask

import (
	"regexp"
	"strings"
)

/*
 * Which pack a task runs as, when the caller did not say (issue #1623).
 *
 * WHY THIS IS A CHOICE THE SERVER MAKES
 * -------------------------------------
 * Because it is a choice nobody could make well from the composer. The two
 * packs read as two products in the UI and are almost the same thing in the
 * sandbox: identical SIF, identical bind mounts, identical --containall flags,
 * identical egress allowlist, identical tool set, identical memory, CPU and
 * pids limits, identical quota and identical metering. Both pack manifests say
 * so in their own words, that "the difference between packs is task framing and
 * default tooling emphasis only". Two places in the whole system branch on the
 * value: engine.materializePack copies packs/<pack>/ into the conversation's
 * working directory, which since issue #1360 is what the agent actually reads
 * into its system prompt, and engine.publishDeckArtifact refuses to publish for
 * anything but the knowledge-work pack. That is the entire difference.
 *
 * So the toggle asked a customer to pick a system prompt, using two words that
 * do not describe what changes, before they had said what they wanted. Reading
 * what they wanted is strictly better information than asking them to
 * pre-classify it.
 *
 * WHY THE DEFAULT IS KNOWLEDGE WORK AND NOT A COIN FLIP
 * -----------------------------------------------------
 * Being wrong is not symmetric, and the asymmetry comes from the packs
 * themselves rather than from taste. The knowledge-work pack ships three skills
 * (doc-layout, deck-generation, code-canvas), it is the only pack whose
 * sessions can publish an artifact, and its own manifest explicitly permits
 * arbitrary shell, build and test commands. A coding request that lands on it
 * therefore still runs; it is framed differently and carries three skills it
 * will not use. A knowledge request that lands on the coding pack loses the
 * deck skill and the publish path outright.
 *
 * One direction costs framing. The other costs a capability. So this function
 * requires positive evidence to LEAVE the default and never to stay in it, and
 * every term below is one whose presence in a work request is overwhelmingly
 * about code. There is no weak tier and no scoring: one strong hit decides it,
 * which is what keeps the rule explainable in one sentence to the person it
 * just surprised. A scored heuristic with a threshold reads as intelligence and
 * is a coin flip with extra steps.
 *
 * WHAT WAS REJECTED, so it is not re-proposed as an improvement
 * -------------------------------------------------------------
 *   - An attached repository or working folder. There is no such field. This
 *     endpoint accepts pack, instructions and project_id, and no run is bound
 *     to a checkout, so the signal does not exist to read.
 *   - The bound project. It exists, but a project is a RAG document collection
 *     that holds source files as readily as contracts, so it correlates weakly
 *     and would make the rule harder to explain for no accuracy.
 *   - Asking a model. A classifier call before every launch spends the
 *     customer's credits, adds latency to submit, and fails with no good
 *     default when the classifier is unreachable. An inferred mode must not
 *     silently change what a run costs, and that one would.
 *
 * BEING WRONG IS VISIBLE AND CORRECTABLE, which is the load-bearing half.
 * The resolved pack goes back on the task, the composer renders it as a line
 * in the run's own progress ("Hive ran this as a coding task."), and the
 * explicit `pack` field stays on the API so the correction control, the
 * scheduler and every existing client can still name one outright.
 */

// scanLimit bounds how much of the instructions this function reads. It
// matches the 64 KiB read limit edge-api already puts on this endpoint's
// body, so on the path a customer can actually reach it truncates nothing;
// it is here because control-plane's own limit is larger and this is an
// input-parsing path that feeds a launch decision. Cutting mid-rune is
// harmless: every pattern below is ASCII, so a broken tail matches nothing
// rather than matching wrongly.
const scanLimit = 64 << 10

// wordPattern splits the instructions into ASCII words. Everything else,
// punctuation, Unicode, emoji and digits attached to letters, is a boundary.
// This is what makes "Gitanjali" not "git" and "repossession" not "repo": a
// substring search would read both as coding requests, and both are the kind
// of ordinary sentence a knowledge-work user types.
var wordPattern = regexp.MustCompile(`[a-z0-9]+`)

// sourceFilePattern matches a filename carrying a source-code extension,
// optionally with a path in front of it. This is structural evidence rather
// than vocabulary, and it is what makes "have a look at server.go" a coding
// request when the sentence contains no coding word at all.
//
// The extension list is deliberately code only. Naming a file is how most
// knowledge-work requests start ("summarise notes.md", "read proposal.pdf"),
// so a list that reached into document formats would send the most common
// knowledge request straight to the coding pack.
var sourceFilePattern = regexp.MustCompile(
	`[a-z0-9_./-]+\.(go|ts|tsx|js|jsx|mjs|cjs|py|rb|rs|java|kt|kts|swift|php|cs|scala|ex|exs|c|cc|cpp|cxx|h|hpp|m|mm|sh|bash|zsh|sql|svelte|vue|css|scss|sass|less|proto|tf|gradle|ipynb)\b`,
)

// codingTerms are the words and phrases that move a task off the default.
// Curated rather than generated: each one had to be a term whose presence in
// a work request is overwhelmingly about code, because a single hit decides
// the launch.
//
// Notable absences, each one a term that reads as coding to an engineer and
// as ordinary English to everyone else: "function" (the function of a
// department), "commit" (commit to a plan), "bug" (a bug in the process),
// "class", "patch", "deploy", "script", "compiler" ("the Compilers of the
// Talmud" is a real title and "compile" alone already covers the request an
// engineer would type). Leaving them out costs a correction on a rare coding
// request; putting them in costs the default on a common knowledge one.
var codingTerms = []string{
	"cargo",
	"codebase",
	"compilation",
	"compile",
	"compiles",
	"compiled",
	"dockerfile",
	"eslint",
	"git",
	"github",
	"gitlab",
	"gofmt",
	"gradle",
	"integration test",
	"lint",
	"linter",
	"makefile",
	"maven",
	"merge conflict",
	"npm",
	"null pointer",
	"pnpm",
	"pull request",
	"pytest",
	"refactor",
	"refactored",
	"refactoring",
	"refactors",
	"regression test",
	"repo",
	"repos",
	"repositories",
	"repository",
	"segfault",
	"segmentation fault",
	"source code",
	"stack trace",
	"stacktrace",
	"syntax error",
	"test suite",
	"traceback",
	"type error",
	"typecheck",
	"unit test",
	"unit tests",
	"yarn",
}

// InferPack resolves the pack for a task whose caller named none. It always
// returns a pack Valid() accepts, for every input including an empty one:
// this feeds a database column with a CHECK constraint on it and a launch
// that fails closed, so there is no "cannot tell" answer to return.
func InferPack(instructions string) Pack {
	text := instructions
	if len(text) > scanLimit {
		text = text[:scanLimit]
	}
	text = strings.ToLower(text)

	// A fenced block is the least ambiguous signal there is: nobody pastes one
	// into a request to have a memo written.
	if strings.Contains(text, "```") {
		return PackCoding
	}

	if sourceFilePattern.MatchString(text) {
		return PackCoding
	}

	// Rejoining the words with single spaces gives word-boundary matching and
	// multi-word phrases from one mechanism, instead of one regexp per term.
	joined := " " + strings.Join(wordPattern.FindAllString(text, -1), " ") + " "
	for _, term := range codingTerms {
		if strings.Contains(joined, " "+term+" ") {
			return PackCoding
		}
	}

	return PackKnowledgeWork
}
