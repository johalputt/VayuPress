package convert

import "testing"

func TestBestContent_HTMLPassthroughPreservesImagesAndLinks(t *testing.T) {
	html := `<figure class="kg-card"><img src="https://images.unsplash.com/p1" alt="Sky"></figure>` +
		`<p>Hello <a href="https://example.com">link</a></p>`
	got := BestContent(html, "", "", "")
	for _, want := range []string{
		`https://images.unsplash.com/p1`,
		`href="https://example.com"`,
		`<p>Hello`,
	} {
		if !contains(got, want) {
			t.Errorf("expected output to contain %q, got: %s", want, got)
		}
	}
}

func TestBestContent_FeatureImagePrepended(t *testing.T) {
	got := BestContent("<p>body</p>", "", "", "https://images.unsplash.com/feature")
	if !contains(got, `<figure><img src="https://images.unsplash.com/feature"`) {
		t.Errorf("feature image not prepended: %s", got)
	}
}

func TestBestContent_FeatureImageNotDuplicated(t *testing.T) {
	url := "https://images.unsplash.com/inline"
	got := BestContent(`<img src="`+url+`">`, "", "", url)
	if count(got, url) != 1 {
		t.Errorf("feature image duplicated: %s", got)
	}
}

func TestBestContent_LexicalToHTML(t *testing.T) {
	lex := `{"root":{"children":[` +
		`{"type":"heading","tag":"h2","children":[{"text":"Title"}]},` +
		`{"type":"paragraph","children":[{"text":"Para"}]},` +
		`{"type":"image","src":"https://images.pixabay.com/x"}]}}`
	got := BestContent("", "", lex, "")
	for _, want := range []string{"<h2>Title</h2>", "<p>Para</p>", "https://images.pixabay.com/x"} {
		if !contains(got, want) {
			t.Errorf("lexical: expected %q, got: %s", want, got)
		}
	}
}

func TestBestContent_MobiledocToHTML(t *testing.T) {
	md := `{"sections":[[1,"p",[[0,[],0,"Para text"]]],[10,0]],` +
		`"cards":[["image",{"src":"https://images.pixabay.com/p3","caption":"Cap"}]]}`
	got := BestContent("", md, "", "")
	for _, want := range []string{"<p>Para text</p>", "https://images.pixabay.com/p3", "<figcaption>Cap</figcaption>"} {
		if !contains(got, want) {
			t.Errorf("mobiledoc: expected %q, got: %s", want, got)
		}
	}
}

func TestBestContent_HTMLPreferredOverEditorFormats(t *testing.T) {
	got := BestContent("<p>rendered</p>", `{"sections":[]}`, `{"root":{}}`, "")
	if got != "<p>rendered</p>" {
		t.Errorf("expected html to win, got: %s", got)
	}
}

func TestBestContent_TextEscaped(t *testing.T) {
	md := `{"sections":[[1,"p",[[0,[],0,"a < b & c"]]]]}`
	got := BestContent("", md, "", "")
	if !contains(got, "a &lt; b &amp; c") {
		t.Errorf("text not escaped: %s", got)
	}
}

func contains(s, sub string) bool {
	return indexOf(s, sub) >= 0
}

func count(s, sub string) int {
	n, i := 0, 0
	for {
		j := indexOf(s[i:], sub)
		if j < 0 {
			return n
		}
		n++
		i += j + len(sub)
	}
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
