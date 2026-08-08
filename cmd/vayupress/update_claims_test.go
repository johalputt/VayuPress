// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/update"
	"github.com/johalputt/vayupress/internal/users"
)

// SECTION 5 AUDIT — the claim half, which was the larger half.
//
// The update path had no authenticity control and three separate surfaces said
// it did: the page called releases "signed" and promised "signature
// verification" regardless of posture, docs/SECURITY.md named Ed25519 against an
// operator-pinned key as THE authenticity mechanism, and docs/UPGRADING.md told
// operators to pin that key. The pipeline has never produced the asset that path
// needs, so following the security documentation broke updates, and ignoring it
// meant nothing was verified at all.
//
// That is L12 again: the artifacts were real, the prose was confident, and no
// gate could tell any of it from the truth. Verification now exists, so the
// words are true — and this file is what keeps them true, because the failure
// mode was never a missing control alone. It was a control and a description
// drifting apart with nothing watching.

// updatePageAsAdmin renders the real Update page.
func updatePageAsAdmin(t *testing.T) string {
	t.Helper()
	a := &App{}
	req := withUser(httptest.NewRequest(http.MethodGet, "/os/update", nil),
		&users.User{ID: "a1", Email: "root@example.com", Role: users.RoleAdmin})
	rec := httptest.NewRecorder()
	a.handleOSUpdate(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("the update page rendered %d", rec.Code)
	}
	return rec.Body.String()
}

// THE GATE. The page must not promise a signature check the code does not run.
//
// Rendered and read, not grepped from the handler source: a source check passes
// a regression that moves the sentence into a comment, and fails an honest
// rewrite that keeps the meaning.
func TestTheUpdatePageDoesNotPromiseWhatIsNotEnforced(t *testing.T) {
	page := updatePageAsAdmin(t)

	// The page may claim a signature only because ApplyVerified requires one.
	// If that requirement is ever removed, this pairing is what fails.
	if !strings.Contains(applySourceRequiresSignature(t), "selectBundleAsset") {
		t.Fatal("ApplyVerified no longer resolves a signature bundle, so every claim " +
			"on the update page about signatures is now false. Either restore the " +
			"requirement or rewrite the page.")
	}

	// The old copy's specific overclaims must not come back.
	for _, gone := range []string{
		"Ed25519 signature against your pinned release key",
		"signature check skipped",
		"pin a release signing key",
	} {
		if strings.Contains(page, gone) {
			t.Errorf("the update page still says %q.\n\nThat described a control that "+
				"did not exist: the pipeline never produced the .sig asset it needed, so "+
				"pinning a key broke updates and leaving it unset verified nothing.", gone)
		}
	}
}

// The page must not tell an operator to do something that breaks their updater.
// VAYU_RELEASE_PUBKEY is optional now and must never be presented as required.
func TestTheUpdatePageDoesNotTellOperatorsToPinAKey(t *testing.T) {
	page := updatePageAsAdmin(t)
	if strings.Contains(page, "VAYU_RELEASE_PUBKEY") {
		t.Error("the update page names VAYU_RELEASE_PUBKEY.\n\n" +
			"It is an optional extra pin, not the thing that makes updates trustworthy, " +
			"and telling operators to set it is what broke their updaters for as long as " +
			"the release pipeline produced no .sig asset.")
	}
}

// docs/SECURITY.md is the document a reader trusts most, so it is held to the
// same rule: it may not describe gates the code does not run.
func TestTheSecurityDocDoesNotDescribeGatesThatDoNotExist(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "SECURITY.md"))
	if err != nil {
		t.Fatalf("read SECURITY.md: %v", err)
	}
	doc := string(b)

	for claim, why := range map[string]string{
		"There is **no** endpoint that downloads, replaces, or restarts": "" +
			"/os/api/update/apply downloads and replaces, and /os/api/update/restart " +
			"restarts. A security document that under-describes the attack surface " +
			"sends a reviewer looking in the wrong place",
		"`VAYU_RELEASE_PUBKEY` present (no key → no apply)": "" +
			"the panel path never required a pinned key, so this gate was not enforced " +
			"on the route almost every operator actually uses",
		"Ed25519 signature over the digest, verified against an operator-pinned key": "" +
			"that path required a .sig asset the release pipeline has never produced",
	} {
		if strings.Contains(doc, claim) {
			t.Errorf("docs/SECURITY.md still claims %q.\n\n%s", claim, why)
		}
	}

	// And it must still name the control that IS enforced, or the document has
	// been emptied rather than corrected.
	for _, want := range []string{"Sigstore", "tag-release.yml", "token.actions.githubusercontent.com"} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/SECURITY.md no longer mentions %q, so it does not describe the "+
				"control that actually runs", want)
		}
	}
}

// The identity in the document must be the identity in the code. Two copies of a
// security-critical constant drift, and the drift is silent: the doc would keep
// describing a policy the binary had stopped enforcing.
func TestTheDocumentedSignerIdentityMatchesTheEnforcedOne(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "SECURITY.md"))
	if err != nil {
		t.Fatalf("read SECURITY.md: %v", err)
	}
	for _, pinned := range []string{update.ReleaseSignerIdentity, update.ReleaseSignerIssuer} {
		if !strings.Contains(string(b), pinned) {
			t.Errorf("docs/SECURITY.md does not name the enforced signer value %q.\n\n"+
				"The document describes a different policy from the one the binary "+
				"applies, and nothing else would notice.", pinned)
		}
	}
}

// applySourceRequiresSignature returns ApplyVerified's body.
//
// This one assertion does read source, deliberately and narrowly: the property
// is "the apply path still resolves a signature", and the alternative — driving
// a real update — would need a genuinely signed release. internal/update owns
// the behavioural proof; this only stops the PAGE claiming a check the apply
// path has stopped making.
func applySourceRequiresSignature(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "internal", "update", "apply.go"))
	if err != nil {
		t.Fatalf("read apply.go: %v", err)
	}
	src := string(b)
	i := strings.Index(src, "func ApplyVerified(")
	if i < 0 {
		t.Fatal("ApplyVerified is gone")
	}
	rest := src[i:]
	if j := regexp.MustCompile(`\nfunc `).FindStringIndex(rest[1:]); j != nil {
		rest = rest[:j[0]+1]
	}
	return rest
}
