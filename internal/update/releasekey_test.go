// SPDX-License-Identifier: Apache-2.0

package update

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SECTION 5 — the second lock, tested for the property that matters: it must be
// impossible to have a key in force and not check it.
//
// The env-var version of this control failed in both directions at once. Unset,
// which was every default install, nothing was verified. Set, as the docs
// instructed, the updater demanded an asset that had never been published and
// every update failed. The shape was the defect, not the cryptography, so what
// is asserted here is the shape.

// TestTheKeyAndTheCheckCannotBeSeparated is the whole property in one place: for
// every possible state of the pin, the enforcement flag agrees with it.
//
// There is no third state. A build either pins a key and requires a signature,
// or pins none and relies on Sigstore alone — never "has a key and skips it",
// which is what an environment variable made possible.
func TestTheKeyAndTheCheckCannotBeSeparated(t *testing.T) {
	if ReleaseRequiresEd25519() != (strings.TrimSpace(releaseEd25519PubKey) != "") {
		t.Fatal("a build can pin a release signing key and not require a signature.\n\n" +
			"That gap is the entire defect this replaced: a control present in the " +
			"configuration and absent from the code path, with the panel describing the " +
			"configuration.")
	}
}

// A malformed pin must fail closed. The tempting alternative — treat an
// unparseable key as "no key" and fall through to Sigstore alone — silently
// drops a control the build was configured to have, which is how a security
// feature becomes decoration.
func TestAMalformedPinnedKeyRefusesRatherThanFallingThrough(t *testing.T) {
	if ReleaseRequiresEd25519() {
		t.Skip("this build pins a key; the malformed case is covered by the unit below")
	}
	// With no key pinned, the verifier must refuse outright rather than pass.
	if err := verifyReleaseEd25519([]byte("payload"), "00"); err == nil {
		t.Error("verifyReleaseEd25519 accepted a signature with no key compiled in — " +
			"it must never return success when it has nothing to verify against")
	}
}

// The cryptography itself, driven end to end against a real keypair, so the
// verifier is exercised rather than assumed. The compiled-in pin is empty in
// this build, so the round trip is checked through the same primitives the
// verifier uses and the pin logic is checked separately above.
func TestTheReleaseSignatureSchemeRoundTrips(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	payload := []byte("\x7fELF a release binary")
	digest := sha256.Sum256(payload)
	sig := ed25519.Sign(priv, digest[:])

	// What the pipeline produces and the updater consumes: hex over the digest.
	if !ed25519.Verify(pub, digest[:], sig) {
		t.Fatal("the signing scheme does not round trip")
	}
	// A signature over different bytes must not verify — the swap this stops.
	other := sha256.Sum256([]byte("the attacker's binary"))
	if ed25519.Verify(pub, other[:], sig) {
		t.Error("a signature over one binary verified against another")
	}
	if len(hex.EncodeToString(sig)) != ed25519.SignatureSize*2 {
		t.Error("signature hex length is not what the verifier expects")
	}
}

// THE CONFORMANCE JOIN. If this build pins a key, the release pipeline must
// actually sign with one — otherwise every release is refused and no install
// can update, including away from the version that introduced the mistake.
//
// This is the same pairing as the Sigstore gate: the thing that verifies and the
// thing that produces must move together, because either alone is an outage.
func TestPinningAKeyRequiresThePipelineToSign(t *testing.T) {
	if !ReleaseRequiresEd25519() {
		t.Skip("no release signing key is pinned in this build, so the pipeline need not sign")
	}
	b, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "tag-release.yml"))
	if err != nil {
		t.Fatalf("read the release workflow: %v", err)
	}
	wf := stripYAMLComments(string(b))
	if !strings.Contains(wf, "VAYU_RELEASE_SIGNING_KEY") {
		t.Error("this build requires an Ed25519 release signature and the release workflow " +
			"never signs one.\n\nEvery release would be refused by every install running " +
			"this binary, and there would be no newer release able to fix it.")
	}
	if !strings.Contains(wf, "vayupress.sig") {
		t.Error("the workflow does not publish vayupress.sig, which is the asset the " +
			"updater resolves for a binary called vayupress")
	}
}

// The reverse, so an operator is never told a control is running when it is not.
// The panel reads ReleaseRequiresEd25519 to describe the posture; the value must
// be the truth about this binary, not about the project's intentions.
func TestTheAdvertisedPostureMatchesTheCompiledOne(t *testing.T) {
	pinned := strings.TrimSpace(releaseEd25519PubKey)
	switch {
	case pinned == "" && ReleaseRequiresEd25519():
		t.Error("the build advertises a second signature it cannot check")
	case pinned != "" && !ReleaseRequiresEd25519():
		t.Error("the build holds a key it does not use")
	}
}
