// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// davFilesHandler is the DAV handler over a Drive engine, signed in by
// address with the password "pw".
func davFilesHandler(t *testing.T) (http.Handler, *Engine, *AccountStore) {
	t.Helper()
	e, s := driveEngine(t)
	if err := s.Create(context.Background(), "dana@example.com", "h", "Dana", "mailbox"); err != nil {
		t.Fatal(err)
	}
	return e.DAVHandler(func(user, password string) (string, bool) { return strings.ToLower(user), password == "pw" }), e, s
}

func dav(h http.Handler, who, method, path, body string, headers ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if who != "" {
		req.SetBasicAuth(who, "pw")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

const davAnkush, davDana = "ankush@example.com", "dana@example.com"

// TestAnAppKeepsListsAndReadsFilesOverWebDAV — a folder made, a file kept
// and kept again, both listed, and the file read back; the Drive page's own
// functions see the same entries, and the first bytes as a version.
func TestAnAppKeepsListsAndReadsFilesOverWebDAV(t *testing.T) {
	h, e, _ := davFilesHandler(t)
	if rec := dav(h, davAnkush, "MKCOL", "/dav/files/Contracts", ""); rec.Code != http.StatusCreated {
		t.Fatalf("MKCOL: %d %s", rec.Code, rec.Body.String())
	}
	if rec := dav(h, davAnkush, http.MethodPut, "/dav/files/Contracts/deal.txt", "the deal"); rec.Code != http.StatusCreated || rec.Header().Get("ETag") == "" {
		t.Fatalf("PUT of a new file: %d %v %s", rec.Code, rec.Header(), rec.Body.String())
	}
	if rec := dav(h, davAnkush, http.MethodPut, "/dav/files/Contracts/deal.txt", "the deal, amended"); rec.Code != http.StatusNoContent {
		t.Fatalf("PUT over the file: %d %s", rec.Code, rec.Body.String())
	}
	top := dav(h, davAnkush, "PROPFIND", "/dav/files/", "", "Depth", "1")
	if top.Code != http.StatusMultiStatus || !strings.Contains(top.Body.String(), "/dav/files/Contracts/") {
		t.Fatalf("PROPFIND of the top: %d %s", top.Code, top.Body.String())
	}
	in := dav(h, davAnkush, "PROPFIND", "/dav/files/Contracts/", "", "Depth", "1")
	if !strings.Contains(in.Body.String(), "/dav/files/Contracts/deal.txt") || !strings.Contains(in.Body.String(), ">17<") {
		t.Fatalf("PROPFIND of the folder lacks the file or its length:\n%s", in.Body.String())
	}
	got := dav(h, davAnkush, http.MethodGet, "/dav/files/Contracts/deal.txt", "")
	if got.Code != http.StatusOK || got.Body.String() != "the deal, amended" {
		t.Fatalf("GET: %d %q", got.Code, got.Body.String())
	}
	list, _ := e.DriveList(ankush, 0)
	if len(list) != 1 || !list[0].IsDir || list[0].Name != "Contracts" {
		t.Fatalf("the Drive page's top: %q", entryNames(list))
	}
	files, _ := e.DriveList(ankush, list[0].ID)
	if len(files) != 1 || files[0].Name != "deal.txt" {
		t.Fatalf("the Drive page's folder: %q", entryNames(files))
	}
	if vs, _ := e.DriveVersions(ankush, files[0].ID); len(vs) != 1 || vs[0].Size != 8 {
		t.Errorf("the file kept over is not a version: %+v", vs)
	}
}

// TestAFileOverWebDAVIsNeverAPage — /dav/files is on the site's own origin:
// an uploaded HTML file comes back as bytes to keep, whatever it is called.
func TestAFileOverWebDAVIsNeverAPage(t *testing.T) {
	h, _, _ := davFilesHandler(t)
	dav(h, davAnkush, http.MethodPut, "/dav/files/page.html", "<script>alert(1)</script>")
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		hd := dav(h, davAnkush, method, "/dav/files/page.html", "").Header()
		if hd.Get("Content-Type") != "application/octet-stream" || hd.Get("X-Content-Type-Options") != "nosniff" ||
			hd.Get("Content-Security-Policy") != "default-src 'none'; sandbox" || !strings.HasPrefix(hd.Get("Content-Disposition"), "attachment") {
			t.Errorf("%s of an HTML file: %v", method, hd)
		}
	}
}

// TestWebDAVIsTheSignedInMailboxsOwn — nothing without a sign-in, and one
// holder's paths are not another's.
func TestWebDAVIsTheSignedInMailboxsOwn(t *testing.T) {
	h, e, _ := davFilesHandler(t)
	dav(h, davAnkush, http.MethodPut, "/dav/files/plan.txt", "ankush's plan")
	for _, method := range []string{http.MethodGet, http.MethodPut, "PROPFIND", http.MethodDelete, "MKCOL"} {
		if rec := dav(h, "", method, "/dav/files/plan.txt", "x"); rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") == "" {
			t.Errorf("%s with no sign-in: %d", method, rec.Code)
		}
	}
	if rec := dav(h, davDana, http.MethodGet, "/dav/files/plan.txt", ""); rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "ankush") {
		t.Errorf("another holder's GET of the path: %d %s", rec.Code, rec.Body.String())
	}
	// And in a folder: however deep a path goes, it names a file in the
	// signed-in mailbox, never in another.
	dav(h, davAnkush, "MKCOL", "/dav/files/Private", "")
	dav(h, davAnkush, "MKCOL", "/dav/files/Private/Deeper", "")
	dav(h, davAnkush, http.MethodPut, "/dav/files/Private/Deeper/salary.txt", "ankush's salary")
	if rec := dav(h, davDana, http.MethodGet, "/dav/files/Private/Deeper/salary.txt", ""); rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "salary") {
		t.Errorf("another holder's GET of a deep path: %d %s", rec.Code, rec.Body.String())
	}
	if rec := dav(h, davDana, "PROPFIND", "/dav/files/Private/", "", "Depth", "infinity"); strings.Contains(rec.Body.String(), "salary") {
		t.Errorf("another holder's listing of a folder names its file:\n%s", rec.Body.String())
	}
	if rec := dav(h, davDana, "PROPFIND", "/dav/files/", "", "Depth", "1"); strings.Contains(rec.Body.String(), "plan.txt") {
		t.Errorf("another holder's listing names the file:\n%s", rec.Body.String())
	}
	dav(h, davDana, http.MethodPut, "/dav/files/plan.txt", "dana's own")
	dav(h, davDana, http.MethodDelete, "/dav/files/plan.txt", "")
	if got := dav(h, davAnkush, http.MethodGet, "/dav/files/plan.txt", "").Body.String(); got != "ankush's plan" {
		t.Errorf("after another holder wrote and deleted the same path, the file reads %q", got)
	}
	if trash, _ := e.DriveTrashed(ReadAsOwner("dana")); len(trash) != 1 {
		t.Errorf("dana's delete is not in dana's Trash: %d entries", len(trash))
	}
	if trash, _ := e.DriveTrashed(ankush); len(trash) != 0 {
		t.Errorf("dana's delete reached ankush's Trash: %d entries", len(trash))
	}
}

// TestAnAppsDeleteGoesToTrash — as the page's does, so a sync gone wrong is
// undone; and the top of the Drive is not deleted.
func TestAnAppsDeleteGoesToTrash(t *testing.T) {
	h, e, _ := davFilesHandler(t)
	dav(h, davAnkush, http.MethodPut, "/dav/files/notes.txt", "keep me")
	if rec := dav(h, davAnkush, http.MethodDelete, "/dav/files/notes.txt", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE: %d %s", rec.Code, rec.Body.String())
	}
	if rec := dav(h, davAnkush, http.MethodGet, "/dav/files/notes.txt", ""); rec.Code != http.StatusNotFound {
		t.Errorf("GET of a deleted file: %d", rec.Code)
	}
	trash, _ := e.DriveTrashed(ankush)
	if len(trash) != 1 || trash[0].Name != "notes.txt" {
		t.Fatalf("Trash after an app's delete: %q", entryNames(trash))
	}
	if got := read(t, e, ankush, trash[0].ID); got != "keep me" {
		t.Errorf("the trashed file reads %q", got)
	}
	if rec := dav(h, davAnkush, http.MethodDelete, "/dav/files/", ""); rec.Code != http.StatusForbidden {
		t.Errorf("DELETE of the top: %d", rec.Code)
	}
	if rec := dav(h, davAnkush, http.MethodDelete, "/dav/files/never.txt", ""); rec.Code != http.StatusNotFound {
		t.Errorf("DELETE of nothing: %d", rec.Code)
	}
}

// TestAnAppMovesRenamesAndCopies — in one request each; a taken name is
// kept unless the app says to write over it, and then it goes to Trash.
func TestAnAppMovesRenamesAndCopies(t *testing.T) {
	h, e, _ := davFilesHandler(t)
	dav(h, davAnkush, "MKCOL", "/dav/files/A", "")
	dav(h, davAnkush, "MKCOL", "/dav/files/B", "")
	dav(h, davAnkush, http.MethodPut, "/dav/files/A/one.txt", "one")
	dav(h, davAnkush, http.MethodPut, "/dav/files/B/two.txt", "two")
	dest := func(p string) []string { return []string{"Destination", "http://example.com" + p} }

	if rec := dav(h, davAnkush, "MOVE", "/dav/files/A/one.txt", "", dest("/dav/files/B/uno.txt")...); rec.Code != http.StatusCreated {
		t.Fatalf("MOVE to another folder under another name: %d %s", rec.Code, rec.Body.String())
	}
	if got := dav(h, davAnkush, http.MethodGet, "/dav/files/B/uno.txt", "").Body.String(); got != "one" {
		t.Fatalf("the moved file reads %q", got)
	}
	if rec := dav(h, davAnkush, http.MethodGet, "/dav/files/A/one.txt", ""); rec.Code != http.StatusNotFound {
		t.Errorf("the file is still where it was: %d", rec.Code)
	}
	if rec := dav(h, davAnkush, "MOVE", "/dav/files/B/uno.txt", "", append(dest("/dav/files/B/two.txt"), "Overwrite", "F")...); rec.Code != http.StatusPreconditionFailed {
		t.Errorf("MOVE onto a taken name, told not to write over it: %d", rec.Code)
	}
	if got := dav(h, davAnkush, http.MethodGet, "/dav/files/B/two.txt", "").Body.String(); got != "two" {
		t.Errorf("the file at the taken name reads %q", got)
	}
	if rec := dav(h, davAnkush, "MOVE", "/dav/files/B/uno.txt", "", append(dest("/dav/files/B/two.txt"), "Overwrite", "T")...); rec.Code != http.StatusNoContent {
		t.Errorf("MOVE onto a taken name, told to write over it: %d %s", rec.Code, rec.Body.String())
	}
	if got := dav(h, davAnkush, http.MethodGet, "/dav/files/B/two.txt", "").Body.String(); got != "one" {
		t.Errorf("after writing over, the name reads %q", got)
	}
	if trash, _ := e.DriveTrashed(ankush); len(trash) != 1 || read(t, e, ankush, trash[0].ID) != "two" {
		t.Errorf("what was written over is not in Trash: %q", entryNames(trash))
	}
	if rec := dav(h, davAnkush, "MOVE", "/dav/files/B", "", dest("/dav/files/B/inside")...); rec.Code != http.StatusForbidden {
		t.Errorf("MOVE of a folder into itself: %d", rec.Code)
	}
	if rec := dav(h, davAnkush, "MOVE", "/dav/files/B/two.txt", "", dest("/dav/files/Nowhere/x.txt")...); rec.Code != http.StatusConflict {
		t.Errorf("MOVE into a folder that is not there: %d", rec.Code)
	}
	if rec := dav(h, davAnkush, "COPY", "/dav/files/B/two.txt", "", dest("/dav/files/A/copy.txt")...); rec.Code != http.StatusCreated {
		t.Fatalf("COPY: %d %s", rec.Code, rec.Body.String())
	}
	if a, b := dav(h, davAnkush, http.MethodGet, "/dav/files/A/copy.txt", "").Body.String(), dav(h, davAnkush, http.MethodGet, "/dav/files/B/two.txt", "").Body.String(); a != "one" || b != "one" {
		t.Errorf("the copy reads %q and its source %q", a, b)
	}
	if rec := dav(h, davAnkush, "COPY", "/dav/files/B", "", dest("/dav/files/C")...); rec.Code != http.StatusForbidden {
		t.Errorf("COPY of a folder: %d", rec.Code)
	}
}

// TestWebDAVKeepsTheStoresRules — the allowance, a name, a folder that is
// not there, and the conditions an app that syncs sends.
func TestWebDAVKeepsTheStoresRules(t *testing.T) {
	h, _, s := davFilesHandler(t)
	if rec := dav(h, davAnkush, http.MethodPut, "/dav/files/Nowhere/x.txt", "x"); rec.Code != http.StatusConflict {
		t.Errorf("PUT into a folder that is not there: %d", rec.Code)
	}
	first := dav(h, davAnkush, http.MethodPut, "/dav/files/a.txt", "one")
	tag := first.Header().Get("ETag")
	if rec := dav(h, davAnkush, http.MethodPut, "/dav/files/a.txt", "two", "If-None-Match", "*"); rec.Code != http.StatusPreconditionFailed {
		t.Errorf("PUT only-if-new over a file: %d", rec.Code)
	}
	if rec := dav(h, davAnkush, http.MethodPut, "/dav/files/a.txt", "two", "If-Match", `"not-its-tag"`); rec.Code != http.StatusPreconditionFailed {
		t.Errorf("PUT if it is still a version it is not: %d", rec.Code)
	}
	if rec := dav(h, davAnkush, http.MethodPut, "/dav/files/new.txt", "x", "If-Match", tag); rec.Code != http.StatusPreconditionFailed {
		t.Errorf("PUT if-match of a file that is not there: %d", rec.Code)
	}
	if got := dav(h, davAnkush, http.MethodGet, "/dav/files/a.txt", "").Body.String(); got != "one" {
		t.Fatalf("after three refused writes the file reads %q", got)
	}
	if rec := dav(h, davAnkush, http.MethodPut, "/dav/files/a.txt", "two", "If-Match", tag); rec.Code != http.StatusNoContent {
		t.Errorf("PUT if it is still the version it is: %d %s", rec.Code, rec.Body.String())
	}
	if err := s.SetQuota(context.Background(), davAnkush, 16); err != nil {
		t.Fatal(err)
	}
	if rec := dav(h, davAnkush, http.MethodPut, "/dav/files/big.bin", strings.Repeat("b", 64)); rec.Code != http.StatusInsufficientStorage {
		t.Errorf("PUT past the allowance: %d", rec.Code)
	}
	if rec := dav(h, davAnkush, "MKCOL", "/dav/files/a.txt", ""); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("MKCOL where a file is: %d", rec.Code)
	}
	if rec := dav(h, davAnkush, http.MethodGet, "/dav/files/", ""); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET of a folder: %d", rec.Code)
	}
}

// TestASealedFileOverWebDAVNeedsTheKey — locked, it is said to be locked,
// not handed out sealed.
func TestASealedFileOverWebDAVNeedsTheKey(t *testing.T) {
	h, e, _ := davFilesHandler(t)
	md, keys := sealedMaildir(t)
	e.maildir = md
	makePrivate(t, keys)
	if rec := dav(h, davDana, http.MethodPut, "/dav/files/secret.txt", "the launch codes"); rec.Code != http.StatusCreated {
		t.Fatalf("PUT to a private mailbox: %d %s", rec.Code, rec.Body.String())
	}
	if got := dav(h, davDana, http.MethodGet, "/dav/files/secret.txt", "").Body.String(); got != "the launch codes" {
		t.Fatalf("with the key open: %q", got)
	}
	keys.CloseHeld(davDana)
	rec := dav(h, davDana, http.MethodGet, "/dav/files/secret.txt", "")
	if rec.Code != http.StatusLocked || strings.Contains(rec.Body.String(), "BEGIN PGP") || strings.Contains(rec.Body.String(), "launch codes") {
		t.Errorf("with the key closed: %d %s", rec.Code, rec.Body.String())
	}
	if !errors.Is(ErrDriveLocked, ErrDriveLocked) {
		t.Fatal("unreachable")
	}
}
