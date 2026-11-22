// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/signin"
	"github.com/johalputt/vayupress/internal/vayushield/challenge"
	"github.com/johalputt/vayupress/internal/vayushield/intel"
)

// solvedLinkProof asks the install for a challenge, as the page's script
// does, and solves it.
func solvedLinkProof(t *testing.T, a *App) (proof, nonce string) {
	t.Helper()
	rec := httptest.NewRecorder()
	a.handleMemberLinkProof(rec, httptest.NewRequest(http.MethodGet, "/members/link-proof", nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("the challenge was answered %d, %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	var p challenge.PoW
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	for i := 0; ; i++ {
		if n := strconv.Itoa(i); challenge.SolutionValid(p.Salt, n, p.Difficulty) {
			return rec.Body.String(), n
		}
	}
}

// postProvedLink asks for a link as the page's script does: the form's
// fields, with the proof as two more.
func postProvedLink(t *testing.T, a *App, ip, email, proof, nonce string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"email": {email}, "proof": {proof}, "nonce": {nonce}}
	req := httptest.NewRequest(http.MethodPost, "/members/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = ip + ":40000"
	rec := httptest.NewRecorder()
	a.handleMemberLogin(rec, req)
	return rec
}

func joinMember(t *testing.T, a *App, email string) {
	t.Helper()
	if _, err := a.members.Upsert(context.Background(), email); err != nil {
		t.Fatal(err)
	}
}

// An address that is not a member is mailed a link only for a solved proof,
// and each proof buys one link. A member is mailed theirs without one, and
// every request is answered the same, so none says who is a member.
func TestAStrangerIsMailedALinkOnlyForAProof(t *testing.T) {
	a := credentialFloodApp(t)
	joinMember(t, a, "member@elsewhere.test")

	bare := postMemberLogin(t, a, "203.0.113.20", "stranger@elsewhere.test")
	if n := countLoginTokens(t, a, "stranger@elsewhere.test"); n != 0 {
		t.Errorf("a stranger was made %d link(s) with no proof", n)
	}
	proof, nonce := solvedLinkProof(t, a)
	postProvedLink(t, a, "203.0.113.21", "proved@elsewhere.test", proof, nonce)
	if n := countLoginTokens(t, a, "proved@elsewhere.test"); n != 1 {
		t.Errorf("a stranger with a solved proof was made %d links, want 1", n)
	}
	postProvedLink(t, a, "203.0.113.22", "replayed@elsewhere.test", proof, nonce)
	if n := countLoginTokens(t, a, "replayed@elsewhere.test"); n != 0 {
		t.Errorf("one proof bought a second link (%d)", n)
	}
	unsolved := strings.Replace(proof, `"difficulty":4`, `"difficulty":1`, 1)
	postProvedLink(t, a, "203.0.113.23", "lowered@elsewhere.test", unsolved, "0")
	if n := countLoginTokens(t, a, "lowered@elsewhere.test"); n != 0 {
		t.Errorf("a proof whose difficulty was lowered bought %d link(s)", n)
	}
	member := postMemberLogin(t, a, "203.0.113.24", "member@elsewhere.test")
	if n := countLoginTokens(t, a, "member@elsewhere.test"); n != 1 {
		t.Errorf("a member was made %d links with no proof, want 1", n)
	}
	if bare.Code != member.Code || bare.Body.String() != member.Body.String() {
		t.Errorf("a refused stranger was answered %d %q, a member %d %q", bare.Code, bare.Body, member.Code, member.Body)
	}
}

// A source that asks for links for signin.LinkStrangers addresses that are
// not members is banned from sign-in for good, proofs or none: the request
// that reaches the number is refused, the ban says why, and the source's own
// member link is refused after it. Another source is still served.
func TestASourceAskingLinksForStrangersIsBannedForGood(t *testing.T) {
	a := credentialFloodApp(t)
	st := withSignins(t, a)
	const sprayer, reader = "198.51.100.30", "203.0.113.31"
	joinMember(t, a, "member@elsewhere.test")

	for i := 1; i <= signin.LinkStrangers; i++ {
		proof, nonce := solvedLinkProof(t, a)
		addr := "target" + strconv.Itoa(i) + "@elsewhere.test"
		postProvedLink(t, a, sprayer, addr, proof, nonce)
		want := 1
		if i == signin.LinkStrangers {
			want = 0
		}
		if n := countLoginTokens(t, a, addr); n != want {
			t.Fatalf("stranger %d was made %d links, want %d", i, n, want)
		}
	}
	var ban *signin.Block
	for _, b := range st.Blocks("") {
		if b.Reason == "link-spraying" {
			ban = &b
		}
	}
	if ban == nil || ban.Kind != "address" || !ban.Until.IsZero() || ban.Label != "198.51.x.x" {
		t.Fatalf("the source was not banned for good: %+v in %+v", ban, st.Blocks(""))
	}
	postMemberLogin(t, a, sprayer, "member@elsewhere.test")
	if n := countLoginTokens(t, a, "member@elsewhere.test"); n != 0 {
		t.Errorf("the banned source was made %d member link(s)", n)
	}
	proof, nonce := solvedLinkProof(t, a)
	postProvedLink(t, a, reader, "reader@elsewhere.test", proof, nonce)
	if n := countLoginTokens(t, a, "reader@elsewhere.test"); n != 1 {
		t.Errorf("another source was made %d links, want 1", n)
	}
}

// A source on the install's own network is never banned this way: behind a
// proxy that does not say who asked, it is every reader at once.
func TestTheInstallsOwnNetworkIsNotBannedForLinks(t *testing.T) {
	a := credentialFloodApp(t)
	st := withSignins(t, a)
	for i := 0; i < 2*signin.LinkStrangers; i++ {
		proof, nonce := solvedLinkProof(t, a)
		postProvedLink(t, a, "127.0.0.1", "new"+strconv.Itoa(i)+"@elsewhere.test", proof, nonce)
	}
	if b := st.Blocks(""); len(b) != 0 {
		t.Fatalf("loopback was banned: %+v", b)
	}
}

// An address this install's mail was refused at for good is not mailed a
// stranger's link again. A softer refusal (4.x, try later) is not that.
func TestAnAddressThatRefusedTheInstallsMailIsNotMailedAgain(t *testing.T) {
	a := credentialFloodApp(t)
	a.vayuMail.HardBounced("nobody@elsewhere.test") // makes the table
	for addr, status := range map[string]string{"gone@elsewhere.test": "5.1.1", "full@elsewhere.test": "4.2.2"} {
		if _, err := dbpkg.DB.Exec(`INSERT INTO vayumail_bounces (mailbox, message_id, recipient, status, reason) VALUES ('noreply@example.com', 'm@example.com', ?, ?, 'no')`, addr, status); err != nil {
			t.Fatal(err)
		}
	}
	for addr, want := range map[string]int{"gone@elsewhere.test": 0, "full@elsewhere.test": 1} {
		proof, nonce := solvedLinkProof(t, a)
		postProvedLink(t, a, "203.0.113.40", addr, proof, nonce)
		if n := countLoginTokens(t, a, addr); n != want {
			t.Errorf("%s was made %d links, want %d", addr, n, want)
		}
	}
}

// However many sources ask, the install mails at most its hourly ceiling of
// strangers, each with its own proof.
func TestTheInstallMailsStrangersUpToItsCeiling(t *testing.T) {
	a := credentialFloodApp(t)
	sent := 0
	const asked = strangerLinksPerHour + 5
	for i := 0; i < asked; i++ {
		proof, nonce := solvedLinkProof(t, a)
		addr := "crowd" + strconv.Itoa(i) + "@elsewhere.test"
		postProvedLink(t, a, "192.0.2."+strconv.Itoa(i+1), addr, proof, nonce)
		sent += countLoginTokens(t, a, addr)
	}
	if sent != strangerLinksPerHour {
		t.Errorf("%d of %d strangers were mailed, want the ceiling of %d", sent, asked, strangerLinksPerHour)
	}
}

// A member row nobody ever confirmed, from before every path stamped one,
// is a stranger to the link's gates: no link without a proof.
func TestAnUnconfirmedMemberIsAStrangerToTheGates(t *testing.T) {
	a := credentialFloodApp(t)
	joinMember(t, a, "typed@elsewhere.test")
	if _, err := dbpkg.DB.Exec(`UPDATE members SET verified_at = NULL WHERE email = ?`, "typed@elsewhere.test"); err != nil {
		t.Fatal(err)
	}
	postMemberLogin(t, a, "203.0.113.50", "typed@elsewhere.test")
	if n := countLoginTokens(t, a, "typed@elsewhere.test"); n != 0 {
		t.Errorf("an unconfirmed member was made %d link(s) with no proof", n)
	}
	proof, nonce := solvedLinkProof(t, a)
	postProvedLink(t, a, "203.0.113.51", "typed@elsewhere.test", proof, nonce)
	if n := countLoginTokens(t, a, "typed@elsewhere.test"); n != 1 {
		t.Errorf("an unconfirmed member with a proof was made %d links, want 1", n)
	}
}

// withDatacenterList gives the app the X4BNet datacenter list, switched on
// and holding cidr, as boot would from its cached copy.
func withDatacenterList(t *testing.T, a *App, cidr string) {
	t.Helper()
	dir := t.TempDir()
	const id = "x4bnet-datacenter"
	sum := sha256.Sum256([]byte(id))
	if err := os.WriteFile(filepath.Join(dir, "intel-"+hex.EncodeToString(sum[:8])+".json"), []byte(cidr+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.shieldIntel = intel.NewFetcher(dir)
	for _, def := range intel.DefaultFeeds() {
		a.shieldIntel.Add(def, def.ID == id)
	}
	a.shieldIntel.LoadCache()
	a.siteSettings = settings.New(dbpkg.DB)
}

// With the switch on, a stranger's link asked for from a datacenter range
// is not sent, proof or none, and answered as a sent one; a member's still
// is, and a stranger from outside the range still gets one. With it off,
// the range is only a weight elsewhere and the link goes.
func TestStrangersLinksFromDatacenterRangesAreRefusedWhenSwitchedOn(t *testing.T) {
	a := credentialFloodApp(t)
	withDatacenterList(t, a, "185.65.10.0/24")
	joinMember(t, a, "member@elsewhere.test")
	ask := func(ip, email string) *httptest.ResponseRecorder {
		proof, nonce := solvedLinkProof(t, a)
		return postProvedLink(t, a, ip, email, proof, nonce)
	}

	ask("185.65.10.20", "off@elsewhere.test")
	if n := countLoginTokens(t, a, "off@elsewhere.test"); n != 1 {
		t.Fatalf("with the switch off a datacenter stranger was made %d links, want 1", n)
	}
	if err := a.siteSettings.SetMany(context.Background(), settings.ForPrimary(), map[string]string{settings.KeyShieldLinkDatacenter: "on"}); err != nil {
		t.Fatal(err)
	}
	refused := ask("185.65.10.21", "dc@elsewhere.test")
	if n := countLoginTokens(t, a, "dc@elsewhere.test"); n != 0 {
		t.Errorf("a stranger's link from a datacenter range was made %d times with the switch on", n)
	}
	sent := ask("203.0.113.70", "home@elsewhere.test")
	if n := countLoginTokens(t, a, "home@elsewhere.test"); n != 1 {
		t.Errorf("a stranger from outside the range was made %d links, want 1", n)
	}
	if refused.Code != sent.Code || refused.Header().Get("Location") != sent.Header().Get("Location") {
		t.Errorf("the refusal answered %d %q, a sent link %d %q", refused.Code, refused.Header().Get("Location"), sent.Code, sent.Header().Get("Location"))
	}
	postMemberLogin(t, a, "185.65.10.22", "member@elsewhere.test")
	if n := countLoginTokens(t, a, "member@elsewhere.test"); n != 1 {
		t.Errorf("a member asking from a datacenter range was made %d links, want 1", n)
	}
}
