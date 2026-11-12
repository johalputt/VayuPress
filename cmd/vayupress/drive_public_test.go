// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// shareVisit is a visit to a link by someone with no session: a GET, or a
// press of the page's button with what its form holds.
func shareVisit(a *App, method, token string, form url.Values) *httptest.ResponseRecorder {
	router := chi.NewRouter()
	router.Get("/drive/s/{token}", a.handleDriveShare)
	router.Post("/drive/s/{token}", a.handleDriveShare)
	var req *http.Request
	if method == http.MethodPost {
		req = httptest.NewRequest(method, "/drive/s/"+token, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req = httptest.NewRequest(method, "/drive/s/"+token, nil)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

var shareLinkRe = regexp.MustCompile(`/drive/s/([0-9a-f]{64})"`)

// makeLink makes a link from the file's sheet, as the holder does, and
// reads its token from the one answer that shows it.
func makeLink(t *testing.T, a *App, id int64, vals url.Values) string {
	t.Helper()
	vals.Set("op", "share")
	vals.Set("id", strconv.FormatInt(id, 10))
	rec := driveAct(a, dana(), vals)
	m := shareLinkRe.FindStringSubmatch(rec.Body.String())
	if m == nil || !strings.Contains(rec.Body.String(), "This link is shown once") {
		t.Fatalf("making a link: %d %s", rec.Code, rec.Body.String())
	}
	return m[1]
}

// TestAHolderSharesAFileByALinkAndStopsIt — made in the sheet, shown once,
// used by a stranger, listed with its count, and stopped.
func TestAHolderSharesAFileByALinkAndStopsIt(t *testing.T) {
	a := calendarApp(t)
	driveUpload(a, dana(), 0, "page.html", "<script>alert(1)</script>")
	id := driveIDs(t, a, "dana", 0)["page.html"]
	token := makeLink(t, a, id, url.Values{"ends": {"7"}})

	page := shareVisit(a, http.MethodGet, token, nil)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `<h1 class="login-title">page.html</h1>`) || !strings.Contains(page.Body.String(), "25 B, shared by dana@example.com") {
		t.Fatalf("the link's page: %d %s", page.Code, page.Body.String())
	}
	if strings.Contains(page.Body.String(), "alert(1)") || strings.Contains(page.Body.String(), `name="password"`) {
		t.Error("the page shows the file, or asks for a password it does not have")
	}
	h := page.Header()
	if h.Get("Cache-Control") != "no-store" || h.Get("Referrer-Policy") != "no-referrer" || !strings.Contains(h.Get("X-Robots-Tag"), "noindex") {
		t.Errorf("the page's headers: %v", h)
	}
	// Opening the address took none of its downloads; the press does.
	links, _ := a.vayuMail.DriveShares(vmail.ReadAsOwner("dana"), id)
	if len(links) != 1 || links[0].Downloads != 0 {
		t.Fatalf("after a visit: %+v", links)
	}
	got := shareVisit(a, http.MethodPost, token, url.Values{})
	h = got.Header()
	if got.Code != http.StatusOK || got.Body.String() != "<script>alert(1)</script>" || h.Get("Content-Length") != "25" {
		t.Fatalf("the press: %d %q %v", got.Code, got.Body.String(), h)
	}
	if h.Get("Content-Type") != "application/octet-stream" || h.Get("Content-Disposition") != `attachment; filename="page.html"` ||
		h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Content-Security-Policy") != "default-src 'none'; sandbox" ||
		h.Get("Cache-Control") != "no-store" || h.Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("a shared HTML file goes out as something a browser may run: %v", h)
	}

	sheet := calGet(a, a.handleVayuOSDriveEntry, "/os/vayumail/drive/entry?id="+strconv.FormatInt(id, 10), dana(), "").Body.String()
	if !strings.Contains(sheet, "Share by link") || !strings.Contains(sheet, "· 1 download<") || strings.Contains(sheet, token) {
		t.Fatalf("the sheet's list of links (which never shows the address again):\n%s", sheet)
	}
	rec := driveAct(a, dana(), url.Values{"op": {"unshare"}, "id": {strconv.FormatInt(id, 10)}, "share": {strconv.FormatInt(links[0].ID, 10)}})
	if !strings.Contains(rec.Body.String(), "That link is stopped") {
		t.Fatalf("stopping it: %s", rec.Body.String())
	}
	for _, m := range []string{http.MethodGet, http.MethodPost} {
		if gone := shareVisit(a, m, token, url.Values{}); gone.Code != http.StatusNotFound || !strings.Contains(gone.Body.String(), "This link is not open") || strings.Contains(gone.Body.String(), "alert(1)") {
			t.Errorf("%s of a stopped link: %d %s", m, gone.Code, gone.Body.String())
		}
	}
}

// TestALinkWithAPasswordAsksForIt — and says a wrong one is wrong, without
// the file.
func TestALinkWithAPasswordAsksForIt(t *testing.T) {
	a := calendarApp(t)
	driveUpload(a, dana(), 0, "plan.txt", "the plan")
	id := driveIDs(t, a, "dana", 0)["plan.txt"]
	token := makeLink(t, a, id, url.Values{"ends": {"0"}, "password": {"correct horse"}, "most": {"1"}})
	if page := shareVisit(a, http.MethodGet, token, nil).Body.String(); !strings.Contains(page, `name="password"`) {
		t.Fatalf("the page does not ask for the password:\n%s", page)
	}
	wrong := shareVisit(a, http.MethodPost, token, url.Values{"password": {"battery staple"}})
	if wrong.Code != http.StatusForbidden || !strings.Contains(wrong.Body.String(), "That is not the password for this link") || strings.Contains(wrong.Body.String(), "the plan") {
		t.Fatalf("a wrong password: %d %s", wrong.Code, wrong.Body.String())
	}
	if got := shareVisit(a, http.MethodPost, token, url.Values{"password": {"correct horse"}}); got.Code != http.StatusOK || got.Body.String() != "the plan" {
		t.Fatalf("the right password: %d %s", got.Code, got.Body.String())
	}
	// One download was its most.
	if again := shareVisit(a, http.MethodPost, token, url.Values{"password": {"correct horse"}}); again.Code != http.StatusNotFound || strings.Contains(again.Body.String(), "the plan") {
		t.Fatalf("a second download of one: %d %s", again.Code, again.Body.String())
	}
	sheet := calGet(a, a.handleVayuOSDriveEntry, "/os/vayumail/drive/entry?id="+strconv.FormatInt(id, 10), dana(), "").Body.String()
	if !strings.Contains(sheet, "· 1 of 1 downloads · password") {
		t.Errorf("the sheet's line for the link:\n%s", sheet)
	}
}

// TestALinksRefusalIsSaidInTheSheet — a password too short makes no link.
func TestALinksRefusalIsSaidInTheSheet(t *testing.T) {
	a := calendarApp(t)
	driveUpload(a, dana(), 0, "plan.txt", "the plan")
	id := driveIDs(t, a, "dana", 0)["plan.txt"]
	rec := driveAct(a, dana(), url.Values{"op": {"share"}, "id": {strconv.FormatInt(id, 10)}, "password": {"short"}})
	if shareLinkRe.MatchString(rec.Body.String()) || !strings.Contains(rec.Body.String(), "a password is at least 8 characters") {
		t.Fatalf("a link with a five-character password: %s", rec.Body.String())
	}
	if links, _ := a.vayuMail.DriveShares(vmail.ReadAsOwner("dana"), id); len(links) != 0 {
		t.Errorf("a refused link was made: %+v", links)
	}
	// Another holder's press on the same file makes none either.
	other := eli(t, a)
	rec = postForm(a.handleVayuOSDriveAction, "/os/vayumail/drive/action", url.Values{"op": {"share"}, "id": {strconv.FormatInt(id, 10)}}.Encode(), other)
	if shareLinkRe.MatchString(rec.Body.String()) {
		t.Errorf("another holder made a link to dana's file: %s", rec.Body.String())
	}
}

// TestAPrivateMailboxsSheetsSayWhatIsSealedAndWhatALinkNeeds — the files
// kept before the mailbox was private are sealed with it, whatever their
// size; and a sealed file's sheet says a link needs a readable copy before
// the holder makes one, since that copy is readable to the server as the
// file is not.
func TestAPrivateMailboxsSheetsSayWhatIsSealedAndWhatALinkNeeds(t *testing.T) {
	a, _, dana := sealedMailApp(t)
	driveUpload(a, dana, 0, "before.txt", "kept before it was private")
	if rec := driveUpload(a, dana, 0, "big.bin", strings.Repeat("b", 25<<20+1)); rec.Code != http.StatusNoContent {
		t.Fatalf("a 25 MB file in a plain mailbox: %d", rec.Code)
	}
	makeDanaPrivate(t, a, dana)
	// The page seals what is stored behind itself; here it is waited for.
	if _, left, err := a.vayuMail.SealStored("dana@example.com"); err != nil || left != 0 {
		t.Fatalf("sealing what was stored: %d left, %v", left, err)
	}
	if rec := driveUpload(a, dana, 0, "after.txt", "the launch codes"); rec.Code != http.StatusNoContent {
		t.Fatalf("an upload to a private mailbox: %d %s", rec.Code, rec.Body.String())
	}
	ids := driveIDs(t, a, "dana", 0)
	sheet := func(name string) string {
		return calGet(a, a.handleVayuOSDriveEntry, "/os/vayumail/drive/entry?id="+strconv.FormatInt(ids[name], 10), dana, "").Body.String()
	}
	const copyNeeded = "A link needs a copy that is not sealed, kept on the server until the link ends or you stop it."
	const notSealed = "This file is not sealed yet"
	for _, name := range []string{"before.txt", "after.txt", "big.bin"} {
		if got := sheet(name); !strings.Contains(got, copyNeeded) || strings.Contains(got, notSealed) || !strings.Contains(got, "· sealed") {
			t.Errorf("%s, a sealed file's sheet:\n%s", name, got)
		}
	}
	if card := a.privateMailboxBlock(withUser(httptest.NewRequest(http.MethodGet, "/", nil), dana), "dana@example.com"); !strings.Contains(card, "Files in Drive are sealed, whatever their size.") {
		t.Errorf("the private card does not say what of Drive is sealed:\n%s", card)
	}
	rec := postForm(a.handleVayuOSDriveAction, "/os/vayumail/drive/action", url.Values{"op": {"share"}, "id": {strconv.FormatInt(ids["after.txt"], 10)}, "ends": {"1"}}.Encode(), dana)
	m := shareLinkRe.FindStringSubmatch(rec.Body.String())
	if m == nil {
		t.Fatalf("sharing a sealed file: %s", rec.Body.String())
	}
	if got := shareVisit(a, http.MethodPost, m[1], url.Values{}); got.Code != http.StatusOK || got.Body.String() != "the launch codes" {
		t.Fatalf("the link to a sealed file: %d %q", got.Code, got.Body.String())
	}
}

// TestABusyLinkSaysSoAndCountsNoDownload — one link has sharePerLink
// sendings at once; a press past them is told to wait, uncounted, and
// another link is not held up.
func TestABusyLinkSaysSoAndCountsNoDownload(t *testing.T) {
	a := calendarApp(t)
	driveUpload(a, dana(), 0, "a.txt", "a")
	driveUpload(a, dana(), 0, "b.txt", "b")
	ids := driveIDs(t, a, "dana", 0)
	busy := makeLink(t, a, ids["a.txt"], url.Values{"ends": {"7"}})
	free := makeLink(t, a, ids["b.txt"], url.Values{"ends": {"7"}})
	var held []func()
	for i := 0; i < sharePerLink; i++ {
		done, ok := shareSlot(busy)
		if !ok {
			t.Fatalf("sending %d of one link refused", i+1)
		}
		held = append(held, done)
	}
	rec := shareVisit(a, http.MethodPost, busy, url.Values{})
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" || !strings.Contains(rec.Body.String(), "This link is busy") {
		t.Fatalf("a press on a busy link: %d %s", rec.Code, rec.Body.String())
	}
	if links, _ := a.vayuMail.DriveShares(vmail.ReadAsOwner("dana"), ids["a.txt"]); links[0].Downloads != 0 {
		t.Errorf("a press told to wait was counted: %+v", links[0])
	}
	if rec := shareVisit(a, http.MethodPost, free, url.Values{}); rec.Code != http.StatusOK || rec.Body.String() != "b" {
		t.Errorf("another link while one is busy: %d %s", rec.Code, rec.Body.String())
	}
	for _, done := range held {
		done()
	}
	if rec := shareVisit(a, http.MethodPost, busy, url.Values{}); rec.Code != http.StatusOK || rec.Body.String() != "a" {
		t.Errorf("after its sendings ended: %d %s", rec.Code, rec.Body.String())
	}
	// And the install's own bound: every place taken by other links.
	held = held[:0]
	for i := 0; len(held) < cap(shareSlots); i++ {
		done, ok := shareSlot("other-" + strconv.Itoa(i/sharePerLink))
		if !ok {
			t.Fatalf("place %d of the install's refused", len(held)+1)
		}
		held = append(held, done)
	}
	if rec := shareVisit(a, http.MethodPost, free, url.Values{}); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("a press with every place taken: %d", rec.Code)
	}
	for _, done := range held {
		done()
	}
	if len(shareSending.n) != 0 || len(shareSlots) != 0 {
		t.Errorf("places not given back: %v, %d", shareSending.n, len(shareSlots))
	}
}
