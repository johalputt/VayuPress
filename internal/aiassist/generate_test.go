// SPDX-License-Identifier: Apache-2.0

package aiassist

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGenerateOpOpenAI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer k123" {
			t.Errorf("Authorization = %q", got)
		}
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "gpt-4o-mini" {
			t.Errorf("model = %v", body["model"])
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"# Hi\n\nHello."}}]}`))
	}))
	defer srv.Close()

	out, _, err := GenerateOpDetail(context.Background(), srv.Client(),
		Backend{Kind: KindOpenAI, Endpoint: srv.URL + "/v1", APIKey: "k123", Model: "gpt-4o-mini"},
		OpDraft, "write about x")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Hello") {
		t.Errorf("got %q", out)
	}
}

func TestGenerateOpOllama(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/api/generate") {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"response":"drafted text"}`))
	}))
	defer srv.Close()

	out, _, err := GenerateOpDetail(context.Background(), srv.Client(),
		Backend{Kind: KindOllama, Endpoint: srv.URL, Model: "llama3.2"}, OpDraft, "prompt")
	if err != nil {
		t.Fatal(err)
	}
	if out != "drafted text" {
		t.Errorf("got %q", out)
	}
}

func TestGenerateOpOpenAIRequiresModel(t *testing.T) {
	if _, _, err := GenerateOpDetail(context.Background(), nil,
		Backend{Kind: KindOpenAI, Endpoint: "http://x/v1"}, OpDraft, "p"); err == nil {
		t.Error("expected an error when no model is set for an OpenAI-compatible backend")
	}
}

func TestDraftOpPromptExists(t *testing.T) {
	p, ok := buildPrompt(OpDraft, "a topic")
	if !ok || !strings.Contains(p, "Markdown") || !strings.Contains(p, "a topic") {
		t.Errorf("draft prompt not built correctly: ok=%v p=%q", ok, p)
	}
	found := false
	for _, op := range SupportedOps() {
		if op == OpDraft {
			found = true
		}
	}
	if !found {
		t.Error("OpDraft missing from SupportedOps")
	}
}

// TestGenerateOpSurfacesProviderError pins that a provider's own explanation
// reaches the caller. OpenRouter answers a rejected request with a JSON body
// explaining exactly why — no credits, model not available to this account,
// rate limited — and discarding it leaves an operator with "it failed" and no
// way to fix their own install.
func TestGenerateOpSurfacesProviderError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"error":{"message":"This model requires credits","code":402}}`))
	}))
	defer srv.Close()
	_, _, err := GenerateOpDetail(context.Background(), srv.Client(),
		Backend{Kind: KindOpenAI, Endpoint: srv.URL, APIKey: "k", Model: "m"}, OpDraft, "write something")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "This model requires credits") {
		t.Errorf("the provider's own reason must reach the caller, got: %v", err)
	}
	if !strings.Contains(err.Error(), "402") {
		t.Errorf("the status code must be reported, got: %v", err)
	}
}

// TestGenerateOpEmptyContentIsAnError pins the silent-empty-draft bug. Reasoning
// models on OpenRouter routinely return an empty "content" with the prose in
// "reasoning"; returning "" with a nil error made that indistinguishable from
// success, so the editor inserted nothing and said the model "returned nothing"
// while the server logged no failure at all.
func TestGenerateOpEmptyContentIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"   "}}]}`))
	}))
	defer srv.Close()
	out, _, err := GenerateOpDetail(context.Background(), srv.Client(),
		Backend{Kind: KindOpenAI, Endpoint: srv.URL, APIKey: "k", Model: "m"}, OpDraft, "write something")
	if err == nil {
		t.Fatalf("empty content must be an error, got output %q and nil error", out)
	}
}

// TestGenerateOpFallsBackToReasoning: when a reasoning model puts its answer in
// "reasoning" and leaves "content" empty, use it rather than failing — the draft
// is there, just under a different key.
func TestGenerateOpFallsBackToReasoning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"","reasoning":"# Title\n\nBody."}}]}`))
	}))
	defer srv.Close()
	out, _, err := GenerateOpDetail(context.Background(), srv.Client(),
		Backend{Kind: KindOpenAI, Endpoint: srv.URL, APIKey: "k", Model: "m"}, OpDraft, "write something")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "# Title") {
		t.Errorf("reasoning content should be used as the draft, got %q", out)
	}
}

// TestGenerateOpErrorBodyOn200 covers the other OpenRouter shape: HTTP 200 with
// an error object and no choices.
func TestGenerateOpErrorBodyOn200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error":{"message":"rate limited upstream"}}`))
	}))
	defer srv.Close()
	_, _, err := GenerateOpDetail(context.Background(), srv.Client(),
		Backend{Kind: KindOpenAI, Endpoint: srv.URL, APIKey: "k", Model: "m"}, OpDraft, "x")
	if err == nil || !strings.Contains(err.Error(), "rate limited upstream") {
		t.Errorf("a 200-with-error body must surface the reason, got: %v", err)
	}
}

// TestProviderErrorScrubsEndpoints: a provider message is shown to the author, so
// it must not carry the operator's gateway address. A custom gateway that echoes
// its own URL in an error would otherwise disclose an internal host.
func TestProviderErrorScrubsEndpoints(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":{"message":"upstream http://10.1.2.3:8080/v1/chat failed for internal-gw.corp:9000"}}`))
	}))
	defer srv.Close()
	_, _, err := GenerateOpDetail(context.Background(), srv.Client(),
		Backend{Kind: KindOpenAI, Endpoint: srv.URL, APIKey: "k", Model: "m"}, OpDraft, "x")
	if err == nil {
		t.Fatal("expected an error")
	}
	var pe *ProviderError
	if !errors.As(err, &pe) {
		t.Fatalf("a provider-reported failure must be a *ProviderError, got %T", err)
	}
	for _, leak := range []string{"10.1.2.3", "http://", "internal-gw.corp:9000", "8080"} {
		if strings.Contains(pe.Message, leak) {
			t.Errorf("scrubbed message still contains %q: %s", leak, pe.Message)
		}
	}
	// The useful part must survive.
	if !strings.Contains(pe.Message, "upstream") || !strings.Contains(pe.Message, "failed") {
		t.Errorf("scrubbing removed the actionable text: %s", pe.Message)
	}
	if pe.Status != http.StatusBadGateway {
		t.Errorf("status should be preserved, got %d", pe.Status)
	}
}

// TestTransportErrorIsNotAProviderError: a connection failure's text contains the
// endpoint URL, so it must NOT be typed as safe-to-display.
func TestTransportErrorIsNotAProviderError(t *testing.T) {
	// Port 1 on loopback refuses connections.
	_, _, err := GenerateOpDetail(context.Background(), &http.Client{},
		Backend{Kind: KindOpenAI, Endpoint: "http://127.0.0.1:1/v1", APIKey: "k", Model: "m"}, OpDraft, "x")
	if err == nil {
		t.Fatal("expected an error")
	}
	var pe *ProviderError
	if errors.As(err, &pe) {
		t.Errorf("a transport error must not be typed as a displayable ProviderError: %v", err)
	}
}

// TestTruncationIsReported: a draft cut off at the token cap is still returned,
// but the caller must be able to tell the author it is incomplete.
func TestTruncationIsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"served/model","choices":[{"finish_reason":"length","message":{"content":"# Half a post"}}]}`))
	}))
	defer srv.Close()
	out, meta, err := GenerateOpDetail(context.Background(), srv.Client(),
		Backend{Kind: KindOpenAI, Endpoint: srv.URL, APIKey: "k", Model: "m"}, OpDraft, "x")
	if err != nil {
		t.Fatalf("a truncated draft is still a draft: %v", err)
	}
	if out == "" {
		t.Error("the partial content should be returned")
	}
	if !meta.Truncated {
		t.Error("truncation must be reported")
	}
	if meta.Model != "served/model" {
		t.Errorf("the served model should be reported, got %q", meta.Model)
	}
}

// TestOptionsAreSent: temperature and max_tokens must reach the provider, or the
// new controls in the editor panel would be decorative.
func TestOptionsAreSent(t *testing.T) {
	var got map[string]interface{}
	var sawReferer, sawTitle bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		if r.Header.Get("HTTP-Referer") != "" {
			sawReferer = true
		}
		if r.Header.Get("X-Title") != "" {
			sawTitle = true
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"<h1>T</h1><p>ok</p>"}}]}`))
	}))
	defer srv.Close()
	_, _, err := GenerateOpDetail(context.Background(), srv.Client(), Backend{
		Kind: KindOpenAI, Endpoint: srv.URL, APIKey: "k", Model: "m",
		Temperature: 0.7, MaxTokens: 1234, Referer: "https://example.org", Title: "VayuPress",
	}, OpDraft, "x")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["temperature"] != 0.7 {
		t.Errorf("temperature not sent: %v", got["temperature"])
	}
	if got["max_tokens"] != float64(1234) {
		t.Errorf("max_tokens not sent: %v", got["max_tokens"])
	}
	if !sawReferer || !sawTitle {
		t.Error("attribution headers should be sent when supplied")
	}
	// Zero values must be omitted so the provider's own defaults apply.
	got = nil
	_, _, _ = GenerateOpDetail(context.Background(), srv.Client(),
		Backend{Kind: KindOpenAI, Endpoint: srv.URL, APIKey: "k", Model: "m"}, OpDraft, "x")
	if _, ok := got["temperature"]; ok {
		t.Error("an unset temperature must not be sent")
	}
	if _, ok := got["max_tokens"]; ok {
		t.Error("an unset max_tokens must not be sent")
	}
}

// TestUnusableCatchesRealGarbage uses output shaped like what a broken free model
// actually produced in the editor: leaked special tokens and a dozen writing
// systems per sentence, inserted as a 2,161-word "draft".
func TestUnusableCatchesRealGarbage(t *testing.T) {
	garbage := `We need to write a blog post about "vreal?" The title says "vensory? ` +
		`System? The workforce says WriterExt type". mismatchOR*ardanRe survive epsilon ` +
		`behaviors on preferential<SPECIAL_205> طلب होताlinar_sent mult FrauخفضModern ` +
		`MY Syn mode花 Surviv 앞서àct retorno삭 invoke aneur experiments`
	bad, why := Unusable(garbage)
	if !bad {
		t.Fatal("this must be refused; it was inserted as a draft")
	}
	if why == "" {
		t.Error("a refusal must explain itself")
	}
	t.Logf("refused with: %s", why)
}

func TestUnusableCatchesLeakedTokens(t *testing.T) {
	for _, s := range []string{
		"<h1>Fine</h1><p>text <SPECIAL_942> more</p>",
		"<|im_start|>assistant Hello",
		"Some text [UNUSED_17] here",
		"Bytes <0xE2> leaked",
	} {
		if bad, _ := Unusable(s); !bad {
			t.Errorf("leaked control vocabulary not caught: %q", s)
		}
	}
}

func TestUnusableCatchesMonologue(t *testing.T) {
	for _, s := range []string{
		"We need to write a blog post about self-hosting. First we should outline.",
		"The user wants an article on email. Let me start with the intro.",
		"Okay, so the user is asking for a guide.",
		"# \nFirst, I need to understand what is being asked here.",
	} {
		if bad, _ := Unusable(s); !bad {
			t.Errorf("model monologue not caught: %q", s)
		}
	}
}

// TestUnusableDoesNotRejectRealPosts is the important half: a false positive
// throws away a good draft, so ordinary prose — including non-English and
// legitimately bilingual prose — must pass.
func TestUnusableDoesNotRejectRealPosts(t *testing.T) {
	ok := []string{
		"<h1>Self-hosting your email</h1><p>Running your own mail server is practical in 2026.</p>",
		"# Self-hosting email\n\nRunning your own mail server is practical.\n\n## Why bother\n\nControl.",
		// Hindi + English, a normal combination for this project's audience.
		"<h1>वायुप्रेस क्या है</h1><p>VayuPress एक self-hosted blog platform है। यह आपके server पर चलता है।</p>",
		// Japanese prose mixes kanji and both kana; that is one language, not salad.
		"<h1>ブログの始め方</h1><p>これは日本語の記事です。サーバーを自分で運用します。</p>",
		// Russian.
		"<h1>Свой почтовый сервер</h1><p>Это статья на русском языке о самостоятельном хостинге.</p>",
		// A post that happens to discuss the phrase, rather than opening with it.
		"<h1>Prompting</h1><p>A weak model often replies \"we need to write a blog post\" instead of writing one.</p>",
	}
	for _, s := range ok {
		if bad, why := Unusable(s); bad {
			t.Errorf("real post refused (%s): %q", why, s)
		}
	}
}

func TestHasHeading(t *testing.T) {
	if !hasHeading("<h1>T</h1><p>b</p>") || !hasHeading("# T\n\nbody") {
		t.Error("headings are structure")
	}
	// This assertion used to require the OPPOSITE, and that is exactly how a
	// monologue reached the editor as a 1,464-word draft: a model reasoning to
	// itself writes in paragraphs too, so paragraph count says nothing.
	if hasHeading("para one\n\npara two\n\npara three") {
		t.Error("paragraphs without a heading are not an article — monologue looks like this")
	}
	if hasHeading("just one run-on stream of thought with no breaks at all") {
		t.Error("a single unbroken blob is not structure")
	}
	// A hashtag is not a heading.
	if hasHeading("Follow #selfhosting for more") {
		t.Error("a hashtag must not count as a heading")
	}
}

// TestStartsLikeArticle is the check that actually closes the monologue hole. It
// is a positive requirement rather than a phrase blacklist, because "Let me
// analyze this instruction" defeated a list that already contained "let me think".
func TestStartsLikeArticle(t *testing.T) {
	good := []string{
		"<h1>Title</h1><p>Body.</p>",
		"  \n<article><h2>Part</h2></article>",
		"```html\n<h1>Fenced</h1>",
		"# Markdown title\n\nBody.",
		"<p>Opens with a paragraph element.</p>",
	}
	for _, g := range good {
		if !StartsLikeArticle(g) {
			t.Errorf("should read as an article opening: %q", g)
		}
	}
	bad := []string{
		"Let me analyze this instruction. The blog engine/platform is \"Kramdown\".",
		"This means we need to produce semantic HTML covering the topic.",
		"Wait, let me re-check the exact wording of the request.",
		"Okay, so the user wants an article about email.",
		"Sure! Here is your article:",
		"#hashtag opening",
	}
	for _, b := range bad {
		if StartsLikeArticle(b) {
			t.Errorf("should NOT read as an article opening: %q", b)
		}
	}
}

// TestRealMonologueFromProductionIsRefused replays the text a model actually put
// into the editor after the first fix shipped — proof the earlier gate was too
// weak, and a regression guard for the phrasing that beat it.
func TestRealMonologueFromProductionIsRefused(t *testing.T) {
	monologue := "Let me analyze this instruction. The blog engine/platform is \"Kramdown\".\n\n" +
		"This means we need to produce semantic HTML (only the article body content, no ```" +
		" wraps, so no markdown fence or leading code block), covering the \"why does the world " +
		"need Kram\"? Wait, let me re-check the exact instruction.\n\n" +
		"So the structure should be an h1, then key takeaways, then sections."
	// It has paragraphs, so the old LooksStructured accepted it.
	if hasHeading(monologue) {
		t.Error("this must not read as an article — it has no heading")
	}
	if StartsLikeArticle(monologue) {
		t.Error("this must not read as an article opening")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		payload, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": "", "reasoning": monologue}}},
		})
		_, _ = w.Write(payload)
	}))
	defer srv.Close()
	_, _, err := GenerateOpDetail(context.Background(), srv.Client(),
		Backend{Kind: KindOpenAI, Endpoint: srv.URL, APIKey: "k", Model: "m"}, OpDraft, "why the world needs Kram")
	if err == nil {
		t.Fatal("the monologue was accepted as a draft again")
	}
	t.Logf("refused with: %v", err)
}

// TestHeadinglessContentIsRefused: the same requirement on the normal content
// path, where a model can equally reply with prose about the request.
func TestHeadinglessContentIsRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"Sure! Here is a long article about email with no headings whatsoever."}}]}`))
	}))
	defer srv.Close()
	if _, _, err := GenerateOpDetail(context.Background(), srv.Client(),
		Backend{Kind: KindOpenAI, Endpoint: srv.URL, APIKey: "k", Model: "m"}, OpDraft, "x"); err == nil {
		t.Error("a headingless reply is not the requested article")
	}
}

func TestLooksLikeHTML(t *testing.T) {
	if !LooksLikeHTML("<h1>x</h1>") || !LooksLikeHTML("<p>hello</p>") || !LooksLikeHTML("<ul><li>a</li></ul>") {
		t.Error("block-level HTML should be detected")
	}
	if LooksLikeHTML("# Heading\n\nSome *emphasis* and a <em>tag</em>.") {
		t.Error("Markdown with an inline tag is not HTML")
	}
	if LooksLikeHTML("") {
		t.Error("empty is not HTML")
	}
}

// TestReasoningMonologueIsRefused: the reasoning field is only a draft when it is
// actually shaped like one. Returning a stream of thought as a 2,000-word post is
// worse than failing, because the author must read it all to find that out.
func TestReasoningMonologueIsRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"","reasoning":"We need to write a blog post about email. Let me think about the structure first."}}]}`))
	}))
	defer srv.Close()
	_, _, err := GenerateOpDetail(context.Background(), srv.Client(),
		Backend{Kind: KindOpenAI, Endpoint: srv.URL, APIKey: "k", Model: "m"}, OpDraft, "x")
	if err == nil {
		t.Fatal("a monologue in the reasoning field must not become a draft")
	}
}

// TestReasoningPostIsAccepted: the flip side — a model that genuinely writes the
// article into the reasoning field still gives the author their draft.
func TestReasoningPostIsAccepted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"","reasoning":"<h1>Email</h1><p>Answer first.</p><h2>Why</h2><p>Because.</p>"}}]}`))
	}))
	defer srv.Close()
	out, meta, err := GenerateOpDetail(context.Background(), srv.Client(),
		Backend{Kind: KindOpenAI, Endpoint: srv.URL, APIKey: "k", Model: "m"}, OpDraft, "x")
	if err != nil {
		t.Fatalf("a structured post in the reasoning field is still a draft: %v", err)
	}
	if !meta.FromReasoning || !strings.Contains(out, "<h1>") {
		t.Errorf("expected the reasoning post, got %q", out)
	}
}

// TestDraftPromptDemandsStructure pins the SEO/GEO contract: an answer engine only
// quotes a passage that stands alone, and a reader only scans a post that has
// headings. Both have to be hard requirements or the model writes an essay.
func TestDraftPromptDemandsStructure(t *testing.T) {
	// The prompt is hard-wrapped, so compare against whitespace-normalised text —
	// otherwise a requirement split across two lines reads as missing.
	p := strings.Join(strings.Fields(draftPrompt("self-hosting email")), " ")
	for _, want := range []string{
		"semantic HTML", "<h1>", "<h2>", "Key takeaways", "Frequently asked questions",
		"first two sentences", "stand alone", "as mentioned above", "no Markdown",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("the draft prompt no longer requires %q", want)
		}
	}
	if !strings.Contains(p, "self-hosting email") {
		t.Error("the author's instruction must reach the model")
	}
	// It must forbid the generic headings that make a post unscannable.
	if !strings.Contains(p, `"Introduction"`) {
		t.Error("the prompt should rule out filler headings")
	}
}

// TestTrimToArticleSalvagesRealShapes: the salvage path matters more than the
// refusals. The commonest reasoning-model reply is thinking FOLLOWED BY the real
// article, and the second commonest is a chat opener before it. Both contain a good
// draft that only needs its lead-in cut.
func TestTrimToArticleSalvagesRealShapes(t *testing.T) {
	cases := []struct{ name, in, wantPrefix string }{
		{"thinking then article",
			"Let me analyze this instruction. We need semantic HTML.\n\nOkay, here goes.\n\n<h1>Self-hosting email</h1><p>Body.</p>",
			"<h1>Self-hosting email</h1>"},
		{"chat opener",
			"Sure! Here is your article:\n\n<h1>Title</h1><p>Body.</p>",
			"<h1>Title</h1>"},
		{"markdown after preamble",
			"Okay, so the user wants a guide.\n\n# Real Title\n\nBody text.",
			"# Real Title"},
		{"already clean is untouched",
			"<h1>Clean</h1><p>Body.</p>",
			"<h1>Clean</h1>"},
	}
	for _, c := range cases {
		got := TrimToArticle(c.in)
		if !strings.HasPrefix(got, c.wantPrefix) {
			t.Errorf("%s: got %q, want prefix %q", c.name, got, c.wantPrefix)
		}
		if !StartsLikeArticle(got) {
			t.Errorf("%s: salvaged text should read as an article opening: %q", c.name, got)
		}
	}
	// Nothing to cut to: left alone so the caller refuses it rather than mangling it.
	blob := "Just thinking out loud with no article anywhere."
	if TrimToArticle(blob) != blob {
		t.Error("text with no heading must be returned unchanged")
	}
}

// TestSalvagedDraftIsAccepted proves the end-to-end effect: a reply that is
// thinking plus an article now yields the article instead of an error.
func TestSalvagedDraftIsAccepted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		payload, _ := json.Marshal(map[string]any{"choices": []map[string]any{{"message": map[string]string{
			"content": "",
			"reasoning": "Let me analyze this instruction. The platform is Kramdown.\n\n" +
				"<h1>Why the world needs Kram</h1><p>Direct answer first.</p><h2>Key takeaways</h2><ul><li>One.</li></ul>",
		}}}})
		_, _ = w.Write(payload)
	}))
	defer srv.Close()
	out, _, err := GenerateOpDetail(context.Background(), srv.Client(),
		Backend{Kind: KindOpenAI, Endpoint: srv.URL, APIKey: "k", Model: "m"}, OpDraft, "x")
	if err != nil {
		t.Fatalf("an article behind a preamble should be salvaged, got: %v", err)
	}
	if !strings.HasPrefix(out, "<h1>Why the world needs Kram</h1>") {
		t.Errorf("the preamble should be gone, got %q", out)
	}
	if strings.Contains(out, "Let me analyze") {
		t.Error("the model's thinking must not survive into the draft")
	}
}

// TestExcludeReasoningIsSentWhenAsked: suppressing the reasoning stream is what
// makes a reasoning model answer in "content" at all, so it must reach the wire.
func TestExcludeReasoningIsSentWhenAsked(t *testing.T) {
	var got map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"<h1>T</h1><p>b</p>"}}]}`))
	}))
	defer srv.Close()
	_, _, _ = GenerateOpDetail(context.Background(), srv.Client(),
		Backend{Kind: KindOpenAI, Endpoint: srv.URL, APIKey: "k", Model: "m", ExcludeReasoning: true}, OpDraft, "x")
	rs, ok := got["reasoning"].(map[string]interface{})
	if !ok || rs["exclude"] != true {
		t.Errorf("reasoning exclusion not sent: %v", got["reasoning"])
	}
	// And it must be absent otherwise, since a strict gateway rejects unknown fields.
	got = nil
	_, _, _ = GenerateOpDetail(context.Background(), srv.Client(),
		Backend{Kind: KindOpenAI, Endpoint: srv.URL, APIKey: "k", Model: "m"}, OpDraft, "x")
	if _, present := got["reasoning"]; present {
		t.Error("the reasoning field must not be sent to providers that were not opted in")
	}
}
