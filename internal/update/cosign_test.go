// SPDX-License-Identifier: Apache-2.0

package update

import (
	"crypto/x509"
	"encoding/asn1"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/testing/ca"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

// SECTION 5 AUDIT — the update path had no authenticity control, and the panel
// said it did.
//
// Every release is cosign-signed and the updater never looked at the signature.
// Its only signature path was Ed25519 against an operator-pinned key, and the
// pipeline has never produced a .sig asset — so pinning the key, which the panel
// and docs/UPGRADING.md both instruct, made every update fail. A default install
// therefore verified a SHA-256 checksum published by the same release, fetched
// over the same connection: that proves the bytes were not mangled in transit
// and says nothing whatever about who published them.
//
// In the attacker's voice, and the reason this is the sharpest surface in the
// product:
//
//	I do not need a bug in your code. I need one push to your release channel —
//	a stolen Actions token, a compromised maintainer laptop, a malicious tag.
//	Then I publish a binary and its matching .sha256, and every install on
//	earth downloads it, verifies it against MY checksum, and execs it as the
//	service user on the operator's next click. Your panel calls that "signed".
//
// The control is the Sigstore signature that already exists on every release,
// pinned to the workflow identity that is allowed to produce one.
//
// These tests use sigstore-go's in-process CA rather than a live release,
// because the verifier must be exercised against forged input too, and there is
// no way to obtain a genuinely forged Sigstore bundle.

const (
	testSAN    = "https://github.com/johalputt/VayuPress/.github/workflows/tag-release.yml@refs/heads/main"
	testIssuer = "https://token.actions.githubusercontent.com"
)

// THE CONTROL. A real release, signed by the workflow that is allowed to sign
// releases, must verify — or the updater is bricked for everyone.
func TestAGenuineReleaseSignatureVerifies(t *testing.T) {
	sigstore, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatalf("virtual sigstore: %v", err)
	}
	binary := []byte("\x7fELF...this is the release binary...")
	entity, err := sigstore.Sign(testSAN, testIssuer, binary)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := verifyCosignEntity(entity, binary, sigstore, testSAN, testIssuer, testVerifyOptions()...); err != nil {
		t.Fatalf("a genuine release signature was refused: %v\n\n"+
			"Enforcing a check that rejects real releases does not make anyone safer — "+
			"it stops every install updating, including away from a vulnerable version.", err)
	}
}

// THE TEN-MINUTE TRAP, and the reason this is not a two-line feature.
//
// Keyless signing certificates live about ten minutes. Every release an operator
// ever installs was signed by a certificate that expired long before they
// clicked Update — the one I decoded from a live release was valid 08:04:30 to
// 08:14:30. A verifier that checks the certificate against time.Now() therefore
// rejects EVERY genuine signature, and the obvious "fix" for that is to stop
// checking expiry, which accepts a stolen certificate forever.
//
// The correct answer is neither: the Rekor entry's integrated time says when the
// signature was made, and the certificate is checked as of THAT moment. The
// timestamp is only trustworthy because the log's signature over it is verified
// first — which is why this is delegated to sigstore-go rather than hand-rolled.
func TestASignatureStillVerifiesLongAfterItsCertificateExpired(t *testing.T) {
	sigstore, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatalf("virtual sigstore: %v", err)
	}
	binary := []byte("a release published a long time ago")

	// Signed a year back — far outside any signing certificate's lifetime.
	entity, err := sigstore.SignAtTime(testSAN, testIssuer, binary, time.Now().Add(-365*24*time.Hour))
	if err != nil {
		t.Fatalf("sign at time: %v", err)
	}
	if err := verifyCosignEntity(entity, binary, sigstore, testSAN, testIssuer, testVerifyOptions()...); err != nil {
		t.Fatalf("a release signed a year ago was refused: %v\n\n"+
			"Signing certificates live ten minutes, so this is the state of EVERY release "+
			"by the time anyone installs it. A verifier that fails here fails always, and "+
			"the tempting repair — ignoring certificate validity — accepts a stolen "+
			"certificate forever.", err)
	}
}

// THE ATTACK. Anyone can run a GitHub Action and get a genuine Sigstore
// signature from a genuine Fulcio certificate. What must not verify is a
// signature from an identity that is not this project's release workflow.
func TestASignatureFromAnotherWorkflowIsRefused(t *testing.T) {
	sigstore, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatalf("virtual sigstore: %v", err)
	}
	binary := []byte("attacker's binary, genuinely signed by attacker")

	for _, imposter := range []string{
		"https://github.com/attacker/VayuPress/.github/workflows/tag-release.yml@refs/heads/main",
		"https://github.com/johalputt/VayuPress/.github/workflows/evil.yml@refs/heads/main",
		"https://github.com/johalputt/VayuPress/.github/workflows/tag-release.yml@refs/heads/attacker-branch",
		"https://github.com/johalputt/OtherRepo/.github/workflows/tag-release.yml@refs/heads/main",
	} {
		entity, serr := sigstore.Sign(imposter, testIssuer, binary)
		if serr != nil {
			t.Fatalf("sign as %s: %v", imposter, serr)
		}
		if err := verifyCosignEntity(entity, binary, sigstore, testSAN, testIssuer, testVerifyOptions()...); err == nil {
			t.Errorf("a binary signed by %q was accepted as a VayuPress release.\n\n"+
				"A Sigstore signature proves SOMEBODY signed it, and anybody can run an "+
				"Action. Without an identity policy the check proves nothing at all — it "+
				"is a signature-shaped ornament.", imposter)
		}
	}
}

// The issuer is half the identity: a certificate for the same-looking SAN from a
// different OIDC provider is a different principal entirely.
func TestASignatureFromAnotherIssuerIsRefused(t *testing.T) {
	sigstore, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatalf("virtual sigstore: %v", err)
	}
	binary := []byte("binary")
	entity, err := sigstore.Sign(testSAN, "https://accounts.google.com", binary)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := verifyCosignEntity(entity, binary, sigstore, testSAN, testIssuer, testVerifyOptions()...); err == nil {
		t.Error("a signature carrying this project's SAN but another OIDC issuer was " +
			"accepted — the SAN is only meaningful together with the issuer that vouched for it")
	}
}

// The signature must be bound to THESE bytes. A valid signature over the real
// release, replayed against a swapped binary, is the whole attack this exists to
// stop.
func TestASignatureDoesNotTransferToDifferentBytes(t *testing.T) {
	sigstore, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatalf("virtual sigstore: %v", err)
	}
	genuine := []byte("the real release binary")
	entity, err := sigstore.Sign(testSAN, testIssuer, genuine)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := verifyCosignEntity(entity, []byte("the attacker's binary"), sigstore, testSAN, testIssuer, testVerifyOptions()...); err == nil {
		t.Error("the genuine release's signature verified against different bytes.\n\n" +
			"That is the swap this control exists to stop: publish a real signature " +
			"beside a replaced binary and every install takes it.")
	}
}

// A signature from a CA this install does not trust must be refused, whatever
// identity it claims — otherwise an attacker just stands up their own Fulcio.
func TestASignatureFromAnUntrustedAuthorityIsRefused(t *testing.T) {
	real, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatalf("virtual sigstore: %v", err)
	}
	rogue, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatalf("rogue sigstore: %v", err)
	}
	binary := []byte("binary")
	entity, err := rogue.Sign(testSAN, testIssuer, binary)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	// Correct identity, correct bytes, wrong root of trust.
	if err := verifyCosignEntity(entity, binary, real, testSAN, testIssuer, testVerifyOptions()...); err == nil {
		t.Error("a signature from an unknown certificate authority was accepted — an " +
			"attacker who can run their own Fulcio would then sign whatever they like")
	}
}

// testVerifyOptions is the production policy MINUS the certificate-transparency
// clause, because sigstore-go's in-process CA does not mint SCTs.
//
// Narrowing a harness to fit a fixture is how a missing control hides, so the
// gap is closed rather than shrugged at: the clause dropped here is covered
// against the real release certificate in the test below. Every other clause of
// releaseVerifyOptions is exercised by the suite above.
func testVerifyOptions() []verify.VerifierOption {
	return []verify.VerifierOption{verify.WithObserverTimestamps(1)}
}

// The production policy demands the signing certificate prove it was published
// to a Certificate Transparency log. That demand is only safe if real release
// certificates actually carry the proof — otherwise enforcement would refuse
// every genuine update, which is an outage wearing a security badge.
//
// Asserted against a certificate taken from a PUBLISHED release rather than one
// this test mints, because the question is what Fulcio really issues.
func TestTheReleaseCertificateCarriesTheProofThisPolicyRequires(t *testing.T) {
	pemBytes, err := os.ReadFile(filepath.Join("testdata", "release_cert.pem"))
	if err != nil {
		t.Fatalf("read the published release certificate: %v", err)
	}
	blk, _ := pem.Decode(pemBytes)
	if blk == nil {
		t.Fatal("fixture is not PEM")
	}
	cert, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// RFC 6962 embedded SCT list.
	sctOID := asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 11129, 2, 4, 2}
	for _, ext := range cert.Extensions {
		if ext.Id.Equal(sctOID) {
			return
		}
	}
	t.Errorf("the published release certificate carries no embedded SCT, but "+
		"releaseVerifyOptions requires one — so enforcement would reject every real "+
		"release.\n\nCertificate SAN: %v", cert.URIs)
}

// The embedded Sigstore trust root must parse, or every update fails closed with
// ErrNoTrustRoot and the operator cannot update their way out of it.
func TestTheEmbeddedTrustRootIsUsable(t *testing.T) {
	tm, err := ReleaseTrustMaterial()
	if err != nil {
		t.Fatalf("the embedded Sigstore trust root is unusable: %v\n\n"+
			"Verification cannot run at all in this state, so no install could take an "+
			"update — including one that would fix it.", err)
	}
	if len(tm.FulcioCertificateAuthorities()) == 0 {
		t.Error("the embedded trust root names no Fulcio certificate authority, so no " +
			"release signature could ever chain to anything")
	}
	if len(tm.RekorLogs()) == 0 {
		t.Error("the embedded trust root names no transparency log — without one there is " +
			"no trustworthy signing timestamp, and every signature looks expired")
	}
}

// The real release certificate must chain to the EMBEDDED trust root. This is
// the join between the two halves: a trust root that parses but does not
// actually anchor this project's releases would pass every other test here and
// refuse every real update.
func TestTheEmbeddedTrustRootAnchorsTheRealReleaseCertificate(t *testing.T) {
	tm, err := ReleaseTrustMaterial()
	if err != nil {
		t.Fatalf("trust material: %v", err)
	}
	pemBytes, err := os.ReadFile(filepath.Join("testdata", "release_cert.pem"))
	if err != nil {
		t.Fatalf("read cert: %v", err)
	}
	blk, _ := pem.Decode(pemBytes)
	cert, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	roots := x509.NewCertPool()
	intermediates := x509.NewCertPool()
	for _, fca := range tm.FulcioCertificateAuthorities() {
		ca, ok := fca.(*root.FulcioCertificateAuthority)
		if !ok {
			continue
		}
		if ca.Root != nil {
			roots.AddCert(ca.Root)
		}
		for _, ic := range ca.Intermediates {
			intermediates.AddCert(ic)
		}
	}
	// CurrentTime inside the certificate's own ten-minute window: the point here
	// is the chain, and every release certificate is expired by now.
	if _, err := cert.Verify(x509.VerifyOptions{
		Roots: roots, Intermediates: intermediates,
		CurrentTime: cert.NotBefore.Add(time.Minute),
		KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning, x509.ExtKeyUsageAny},
	}); err != nil {
		t.Errorf("a real release certificate does not chain to the embedded trust root: %v\n\n"+
			"Enforcement would then refuse every genuine release while still accepting "+
			"nothing — an install that can never update again.", err)
	}
}
