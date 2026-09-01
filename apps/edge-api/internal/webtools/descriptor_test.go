package webtools

import (
	"encoding/json"
	"strings"
	"testing"
)

// A5. The serialized tool block for both tools together stays under 1,200
// bytes. The number is not arbitrary: the compose comment on
// HIVE_DEFAULT_FUNCTION_CALLING records that upstream's 21 builtin specs
// serialize to 12,089 bytes and 3,144 Groq prompt tokens for a one word
// answer, which is what froze this deployment on the legacy path. This
// assertion is the affordability bar that lets native mode back on.
func TestDescriptorsFitTheAdvertisementBudget(t *testing.T) {
	blob, err := json.Marshal(Descriptors())
	if err != nil {
		t.Fatalf("marshalling descriptors: %v", err)
	}
	if len(blob) > MaxDescriptorBytes {
		t.Fatalf("the two tool specs serialize to %d bytes, over the %d byte budget:\n%s",
			len(blob), MaxDescriptorBytes, blob)
	}
	t.Logf("two tool specs serialize to %d bytes (budget %d)", len(blob), MaxDescriptorBytes)
}

func TestDescriptorsAreExactlyTheTwoTools(t *testing.T) {
	got := Descriptors()
	if len(got) != 2 {
		t.Fatalf("got %d descriptors, want exactly 2", len(got))
	}
	names := map[string]bool{}
	for _, d := range got {
		if d.Type != "function" {
			t.Errorf("descriptor %q type = %q, want function", d.Function.Name, d.Type)
		}
		if d.Function.Description == "" {
			t.Errorf("descriptor %q has no description", d.Function.Name)
		}
		names[d.Function.Name] = true
	}
	for _, want := range []string{ToolWebSearch, ToolWebFetch} {
		if !names[want] {
			t.Errorf("descriptor %q is missing", want)
		}
	}
}

// The fetch description must tell the model that returned page content is
// data, not instructions (spec section 7, prompt injection containment).
func TestFetchDescriptorMarksContentUntrusted(t *testing.T) {
	for _, d := range Descriptors() {
		if d.Function.Name != ToolWebFetch {
			continue
		}
		if !strings.Contains(strings.ToLower(d.Function.Description), "untrusted") {
			t.Fatalf("web_fetch description does not mark its content untrusted: %q", d.Function.Description)
		}
		return
	}
	t.Fatal("no web_fetch descriptor")
}
