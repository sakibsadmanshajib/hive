// Package deckgen renders a slide deck definition into a single
// self-contained HTML document (inline CSS, inline JS, no external
// requests) for the deck-generation knowledge-work-pack skill. The output
// is published through apps/agent-engine/internal/artifactsclient and
// previewed under the artifacts API's CSP
// (apps/edge-api/internal/artifacts: connect-src 'none', restricted
// frame-ancestors), so it must never reference an external script or
// stylesheet.
package deckgen

import (
	"errors"
	"fmt"
	"html/template"
	"strings"
)

// Slide is one slide: a title plus bullet points.
type Slide struct {
	Title   string
	Bullets []string
}

// Deck is a full slide deck.
type Deck struct {
	Title  string
	Slides []Slide
}

// Render validates d and returns a self-contained HTML document: one
// <section class="slide"> per slide, shown one at a time, advanced by
// arrow keys or click via a small inline script. All slide content is
// passed through html/template, which auto-escapes it, so agent- or
// tenant-authored titles and bullets can never break out of the markup.
func Render(d Deck) (string, error) {
	if strings.TrimSpace(d.Title) == "" {
		return "", errors.New("deckgen: deck title must not be blank")
	}
	if len(d.Slides) == 0 {
		return "", errors.New("deckgen: deck must have at least one slide")
	}
	for i, s := range d.Slides {
		if strings.TrimSpace(s.Title) == "" {
			return "", fmt.Errorf("deckgen: slide %d has no title", i)
		}
	}

	var buf strings.Builder
	if err := deckTemplate.Execute(&buf, d); err != nil {
		return "", fmt.Errorf("deckgen: render: %w", err)
	}
	return buf.String(), nil
}

var deckTemplate = template.Must(template.New("deck").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>{{.Title}}</title>
<style>
  body { margin: 0; font-family: system-ui, sans-serif; background: #111; color: #eee; }
  .slide { display: none; box-sizing: border-box; min-height: 100vh; padding: 8vh 10vw; }
  .slide.active { display: block; }
  .slide h1 { font-size: 2.5rem; margin-bottom: 1.5rem; }
  .slide ul { font-size: 1.4rem; line-height: 1.8; }
  .deck-title { position: fixed; top: 0.5rem; left: 1rem; opacity: 0.5; font-size: 0.9rem; }
</style>
</head>
<body>
<div class="deck-title">{{.Title}}</div>
{{range $i, $slide := .Slides}}<section class="slide{{if eq $i 0}} active{{end}}" data-index="{{$i}}">
  <h1>{{$slide.Title}}</h1>
  <ul>{{range $slide.Bullets}}<li>{{.}}</li>{{end}}</ul>
</section>
{{end}}
<script>
(function () {
  var slides = document.querySelectorAll('.slide');
  var current = 0;
  function show(n) {
    if (n < 0 || n >= slides.length) return;
    slides[current].classList.remove('active');
    current = n;
    slides[current].classList.add('active');
  }
  document.addEventListener('keydown', function (e) {
    if (e.key === 'ArrowRight' || e.key === ' ') show(current + 1);
    if (e.key === 'ArrowLeft') show(current - 1);
  });
  document.addEventListener('click', function () { show(current + 1); });
})();
</script>
</body>
</html>
`))
