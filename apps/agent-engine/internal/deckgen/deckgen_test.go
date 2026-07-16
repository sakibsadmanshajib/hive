package deckgen

import "testing"
import "strings"

func TestRender_ProducesSelfContainedHTML(t *testing.T) {
	deck := Deck{
		Title: "Q3 Roadmap",
		Slides: []Slide{
			{Title: "Overview", Bullets: []string{"one", "two"}},
			{Title: "Next steps", Bullets: []string{"three"}},
		},
	}

	html, err := Render(deck)
	if err != nil {
		t.Fatalf("Render returned error: %v", err)
	}

	for _, want := range []string{"<!DOCTYPE html", "Q3 Roadmap", "Overview", "one", "two", "Next steps", "three"} {
		if !strings.Contains(html, want) {
			t.Fatalf("output missing %q\n---\n%s", want, html)
		}
	}
	if strings.Contains(html, "<script src=") {
		t.Fatal("output references an external script; artifacts must be self-contained")
	}
	if strings.Contains(html, `href="http`) {
		t.Fatal("output references an external stylesheet; artifacts must be self-contained")
	}
	if !strings.Contains(html, "<script>") {
		t.Fatal("output has no inline navigation script")
	}
}

func TestRender_EscapesSlideContent(t *testing.T) {
	deck := Deck{
		Title: "Escaping",
		Slides: []Slide{
			{Title: "<script>alert(1)</script>", Bullets: []string{"<img src=x onerror=alert(1)>"}},
		},
	}

	html, err := Render(deck)
	if err != nil {
		t.Fatalf("Render returned error: %v", err)
	}
	if strings.Contains(html, "<script>alert(1)</script>") {
		t.Fatal("slide title was not escaped")
	}
	if strings.Contains(html, "<img src=x onerror=alert(1)>") {
		t.Fatal("bullet content was not escaped")
	}
}

func TestRender_RejectsEmptyTitle(t *testing.T) {
	deck := Deck{Slides: []Slide{{Title: "One"}}}
	if _, err := Render(deck); err == nil {
		t.Fatal("expected error for empty deck title, got nil")
	}
}

func TestRender_RejectsNoSlides(t *testing.T) {
	deck := Deck{Title: "Empty deck"}
	if _, err := Render(deck); err == nil {
		t.Fatal("expected error for a deck with no slides, got nil")
	}
}

func TestRender_RejectsSlideWithoutTitle(t *testing.T) {
	deck := Deck{Title: "Deck", Slides: []Slide{{Bullets: []string{"a"}}}}
	if _, err := Render(deck); err == nil {
		t.Fatal("expected error for a slide with no title, got nil")
	}
}
