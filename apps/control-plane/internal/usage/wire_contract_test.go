package usage

import (
	"os"
	"regexp"
	"testing"
)

// The console renders this bucket, so the Go constant and the TypeScript one
// are a single contract written in two places. Nothing generated bridges
// them: no OpenAPI client, no shared schema. A rename on either side would
// leave the console falling straight back to its deleted-key label for every
// unattributed row, silently and with every test still green, which is the
// failure shape this repository keeps re-learning.
//
// So this reads the console file and pins the two literals to each other. It
// needs no database and runs in the plain -short leg.
func TestUnattributedGroupKeyMatchesTheConsole(t *testing.T) {
	const consoleFile = "../../../../apps/web-console/lib/control-plane/contract.ts"

	source, err := os.ReadFile(consoleFile)
	if err != nil {
		t.Fatalf("read %s: %v (if the console moved this file, update both sides deliberately)", consoleFile, err)
	}

	match := regexp.MustCompile(`UNATTRIBUTED_GROUP_KEY\s*=\s*"([^"]*)"`).FindSubmatch(source)
	if match == nil {
		t.Fatalf("%s no longer declares UNATTRIBUTED_GROUP_KEY as a string literal", consoleFile)
	}

	if got := string(match[1]); got != UnattributedGroupKey {
		t.Fatalf("group key contract drifted: Go has %q, the console has %q. Both sides render the same bucket, so they have to be the same string.",
			UnattributedGroupKey, got)
	}
}
