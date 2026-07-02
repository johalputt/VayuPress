package bizsite

import (
	"strings"
	"testing"
)

// TestCatalogue guards the product promise: at least 10 complete, mutually
// distinct business templates, each with believable default content.
func TestCatalogue(t *testing.T) {
	all := All()
	if len(all) < 10 {
		t.Fatalf("want >=10 templates, got %d", len(all))
	}
	seenKey, seenCSS := map[string]bool{}, map[string]string{}
	for _, tpl := range all {
		if tpl.Key == "" || tpl.Name == "" || tpl.Category == "" || tpl.CSS == "" {
			t.Errorf("template %q incomplete", tpl.Key)
		}
		if seenKey[tpl.Key] {
			t.Errorf("duplicate key %q", tpl.Key)
		}
		seenKey[tpl.Key] = true
		if other, dup := seenCSS[tpl.CSS]; dup {
			t.Errorf("templates %q and %q share identical CSS", tpl.Key, other)
		}
		seenCSS[tpl.CSS] = tpl.Key
		if tpl.Defaults.Name == "" || tpl.Defaults.Tagline == "" || len(tpl.Defaults.Services) == 0 {
			t.Errorf("template %q lacks default content", tpl.Key)
		}
		if strings.Contains(tpl.CSS, "gradient") {
			t.Errorf("template %q uses a gradient — the catalogue is flat-colour only", tpl.Key)
		}
	}
}

// TestRenderEscapes proves operator strings cannot break out of the HTML.
func TestRenderEscapes(t *testing.T) {
	tpl := All()[0]
	c := Content{
		Name:     `Evil<script>alert(1)</script>`,
		Tagline:  `"><img src=x onerror=1>`,
		About:    "<b>bold</b>",
		Services: []Service{{Title: `<svg onload=1>`, Price: `<i>`}},
		Gallery:  []string{`https://x/y.jpg" onerror="1`},
		ShowBlog: true,
	}
	out := Render(tpl, c, "https://blog.example.com")
	// Raw markup from operator input must never survive; only escaped text may.
	for _, bad := range []string{"<script>alert", "<img src=x", "<svg onload", "<b>bold</b>", `.jpg" onerror`} {
		if strings.Contains(out, bad) {
			t.Errorf("unescaped operator input reached output: %q", bad)
		}
	}
	if !strings.Contains(out, "https://blog.example.com") {
		t.Error("blog link missing")
	}
}

// TestRenderSections proves every content section renders when populated.
func TestRenderSections(t *testing.T) {
	for _, tpl := range All() {
		out := Render(tpl, tpl.Defaults, "/")
		for _, want := range []string{"vb-nav", "vb-hero", "vb-service", "vb-contact", "vb-footer", "vb--" + tpl.Key} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: rendered page missing %q", tpl.Key, want)
			}
		}
		if css := CSS(tpl); !strings.Contains(css, "vb--"+tpl.Key) {
			t.Errorf("%s: CSS does not scope to its template class", tpl.Key)
		}
	}
}

// TestParseContent tolerates junk and round-trips fields.
func TestParseContent(t *testing.T) {
	if c := ParseContent(""); c.Name != "" {
		t.Error("empty input must yield zero content")
	}
	if c := ParseContent("{not json"); c.Name != "" {
		t.Error("bad json must yield zero content")
	}
	c := ParseContent(`{"name":"A","services":[{"title":"T","price":"$1"}],"showBlog":true}`)
	if c.Name != "A" || len(c.Services) != 1 || !c.ShowBlog {
		t.Errorf("round-trip mismatch: %+v", c)
	}
}
