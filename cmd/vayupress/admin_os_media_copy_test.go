// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// extractTag returns the single element that starts with open and ends at the
// first '>' after it, or "" when there is none.
//
// Extracting the one element and asserting on it, rather than searching the
// whole page for a phrase: a page-wide search for "SVG" passes on any page that
// mentions the word somewhere else, and a page-wide search for "refused" cannot
// say which control it found.
func extractTag(page, open string) string {
	i := strings.Index(page, open)
	if i < 0 {
		return ""
	}
	rest := page[i:]
	j := strings.Index(rest, ">")
	if j < 0 {
		return ""
	}
	return rest[:j+1]
}

// extractElement returns the text of the first element opening with open, up to
// the first closing tag after it.
func extractElement(page, open string) string {
	i := strings.Index(page, open)
	if i < 0 {
		return ""
	}
	rest := page[i:]
	j := strings.Index(rest, "</div>")
	if j < 0 {
		return ""
	}
	return rest[:j]
}

// The Media page tells the operator what the library accepts, and it has to be
// what the library accepts.
//
// The dropzone hint read "SVG is refused for security" and the file picker's
// accept list left SVG out, while the handler behind that same dropzone accepts
// SVG, sanitises it, stores it, lists it in the grid above and charges it
// against MEDIA_QUOTA_GB. The connector tool sharing that path advertises SVG in
// its description, so the two surfaces of one feature contradicted each other.
//
// The consequence is not a typo. It lands at the worst moment: when the library
// hits its ceiling the refusal tells the operator to delete files from Media,
// and the grid in front of them shows assets in a format the same page says
// cannot be there. A page that is wrong about what it holds is not a page anyone
// can use to get out of a full library — and "the panel is the way out" is the
// entire justification for having a ceiling at all.
func TestMediaPageDescribesWhatTheUploadActuallyAccepts(t *testing.T) {
	rec := httptest.NewRecorder()
	(&App{}).handleOSMedia(rec, httptest.NewRequest(http.MethodGet, "/os/media", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("Media page returned %d", rec.Code)
	}
	page := rec.Body.String()

	hint := extractElement(page, `<div class="media-dropzone__hint`)
	if hint == "" {
		t.Fatal("could not find the dropzone hint on the Media page")
	}
	if strings.Contains(strings.ToLower(hint), "svg is refused") {
		t.Errorf("the dropzone hint says SVG is refused: %q.\n"+
			"The handler it labels accepts SVG, sanitises it and stores it. An operator who "+
			"believes this line will not look for the SVGs holding their quota down.", hint)
	}
	if !strings.Contains(strings.ToUpper(hint), "SVG") {
		t.Errorf("the dropzone hint lists the accepted formats without SVG: %q.\n"+
			"It is accepted, it is charged against the quota and it is shown in the grid — a "+
			"format the page will store and will not name is a format the operator cannot "+
			"account for.", hint)
	}

	input := extractTag(page, `<input type="file" data-media-input`)
	if input == "" {
		t.Fatal("could not find the media file input on the Media page")
	}
	if !strings.Contains(input, "image/svg+xml") {
		t.Errorf("the file picker's accept list omits image/svg+xml: %q.\n"+
			"A picker that hides the format is the same false claim as the hint, in the one "+
			"place the operator cannot argue with it.", input)
	}
}

// The refusal an operator sees when a file is the wrong type has to name the
// same set of types the path accepts.
//
// It listed PNG, JPEG, GIF, WebP and PDF. SVG is accepted on this exact path, so
// an operator whose SVG failed the sanitiser — the one real reason an SVG is
// refused here — was told the format itself is not allowed, and the fix they
// would then attempt (convert it to something else) is not the fix.
func TestUnsupportedTypeRefusalNamesEveryFormatThisPathAccepts(t *testing.T) {
	mediaQuotaDir(t, 1<<20)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", "notes.txt")
	if err != nil {
		t.Fatalf("build multipart part: %v", err)
	}
	if _, err := part.Write([]byte("this is not an image, a document or a drawing")); err != nil {
		t.Fatalf("write multipart part: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/media", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	(&App{}).handleMediaUpload(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("uploading a text file returned %d, want 415: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad error response: %v (%s)", err, rec.Body.String())
	}
	if !strings.Contains(strings.ToUpper(out.Error), "SVG") {
		t.Errorf("the 415 lists the allowed types as %q, leaving out SVG.\n"+
			"This is the message an operator gets when an SVG fails the sanitiser, and it tells "+
			"them the format is not allowed at all — so they stop trying instead of fixing the "+
			"file, and the panel and the connector describe two different products.", out.Error)
	}
}
