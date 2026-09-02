package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The shipped packs (apps/agent-engine/packs) are only useful if they are laid
// out the way the vendored OpenHands SDK loads a project: Launch copies a pack
// verbatim into the conversation's working directory (issue #1360), and from
// there the loader picks up exactly two shapes
// (vendor/openhands/openhands-sdk/openhands/sdk/skills/skill.py):
//
//   - <root>/AGENTS.md, loaded as an always-active repo skill.
//   - <root>/.agents/skills/<name>/SKILL.md, listed to the model as an
//     available skill, with <name> required to match the frontmatter name
//     (utils.validate_skill_name, strict).
//
// A markdown file anywhere else is either ignored or, if it is named AGENTS.md
// in a subdirectory, turned into a path rule that forces
// disable_model_invocation: injected only if the agent happens to touch that
// directory, and never listed. That is what the three knowledge-work skills
// used to be, which is why nothing surfaced them.
const packsDir = "../../packs"

func TestPacks_UseTheLayoutTheSDKActuallyLoads(t *testing.T) {
	entries, err := os.ReadDir(packsDir)
	if err != nil {
		t.Fatalf("read packs dir: %v", err)
	}
	packs := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		packs++
		root := filepath.Join(packsDir, entry.Name())

		if _, err := os.Stat(filepath.Join(root, "AGENTS.md")); err != nil {
			t.Errorf("pack %s has no root AGENTS.md: %v", entry.Name(), err)
		}
		// The pre-#1360 layout. Its files load as path rules at best.
		if _, err := os.Stat(filepath.Join(root, "skills")); err == nil {
			t.Errorf("pack %s still keeps skills in %s/skills; skills must live in "+
				".agents/skills/<name>/SKILL.md to be listed to the model", entry.Name(), entry.Name())
		}

		skillsDir := filepath.Join(root, ".agents", "skills")
		skills, err := os.ReadDir(skillsDir)
		if os.IsNotExist(err) {
			continue // a pack may legitimately ship no skills (coding-pack does not).
		}
		if err != nil {
			t.Fatalf("read %s: %v", skillsDir, err)
		}
		for _, skill := range skills {
			assertSkillNameMatchesDir(t, filepath.Join(skillsDir, skill.Name()), skill.Name())
		}
	}
	if packs == 0 {
		t.Fatalf("no packs found under %s", packsDir)
	}
}

// The three skills the knowledge-work pack is supposed to offer. Named
// explicitly because engine.go's own deck flow reads .hive/deck.json, a file
// only the deck-generation skill ever tells the agent to write.
func TestPacks_KnowledgeWorkShipsItsThreeSkills(t *testing.T) {
	for _, skill := range []string{"deck-generation", "doc-layout", "code-canvas"} {
		path := filepath.Join(packsDir, packKnowledgeWork, ".agents", "skills", skill, "SKILL.md")
		if _, err := os.Stat(path); err != nil {
			t.Errorf("knowledge-work-pack skill %s is not at %s: %v", skill, path, err)
		}
	}
}

// assertSkillNameMatchesDir checks the one frontmatter field the SDK validates
// in strict mode: a SKILL.md whose name does not equal its directory name
// raises SkillValidationError and the skill is dropped with a log line nobody
// reads.
func assertSkillNameMatchesDir(t *testing.T, dir, want string) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		t.Errorf("skill %s has no SKILL.md: %v", want, err)
		return
	}
	lines := strings.Split(string(body), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		t.Errorf("skill %s opens with no frontmatter block", want)
		return
	}
	// Only the opening block counts. A `name:` line further down is body
	// text, and matching it would let this test pass over a skill the SDK
	// rejects for having no frontmatter name at all.
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			break
		}
		if name, ok := strings.CutPrefix(line, "name:"); ok {
			if got := strings.TrimSpace(name); got != want {
				t.Errorf("skill %s declares name %q; the SDK requires it to equal the directory name", want, got)
			}
			return
		}
	}
	t.Errorf("skill %s declares no frontmatter name", want)
}
