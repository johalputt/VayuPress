// SPDX-License-Identifier: Apache-2.0

package update

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sigstore/sigstore-go/pkg/bundle"
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
	if err := verifyCosignEntity(entity, sha256.Sum256(binary), sigstore, testSAN, testIssuer, testVerifyOptions()...); err != nil {
		t.Fatalf("a genuine release signature was refused: %v\n\n"+
			"Enforcing a check that rejects real releases does not make anyone safer — "+
			"it stops every install updating, including away from a vulnerable version.", err)
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
		if err := verifyCosignEntity(entity, sha256.Sum256(binary), sigstore, testSAN, testIssuer, testVerifyOptions()...); err == nil {
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
	if err := verifyCosignEntity(entity, sha256.Sum256(binary), sigstore, testSAN, testIssuer, testVerifyOptions()...); err == nil {
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
	if err := verifyCosignEntity(entity, sha256.Sum256([]byte("the attacker's binary")), sigstore, testSAN, testIssuer, testVerifyOptions()...); err == nil {
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
	if err := verifyCosignEntity(entity, sha256.Sum256(binary), real, testSAN, testIssuer, testVerifyOptions()...); err == nil {
		t.Error("a signature from an unknown certificate authority was accepted — an " +
			"attacker who can run their own Fulcio would then sign whatever they like")
	}
}

// testVerifyOptions is the production policy MINUS the certificate-transparency
// clause, because sigstore-go's in-process CA does not mint SCTs.
//
// Narrowing a harness to fit a fixture is how a missing control hides, so the
// gap is closed rather than shrugged at: the clause dropped here is covered
// against the real release certificate below. Every other clause of
// releaseVerifyOptions is exercised by the suite above.
func testVerifyOptions() []verify.VerifierOption {
	return []verify.VerifierOption{
		verify.WithTransparencyLog(1),
		verify.WithObserverTimestamps(1),
	}
}

// The clause testVerifyOptions drops must still be PROVEN to be in force, or
// dropping it there quietly makes it optional everywhere.
//
// This turns the harness's limitation into the assertion: the in-process CA
// issues certificates with no embedded SCT, so a signature it produces — genuine
// in every other respect, right identity, right bytes, right trust root — must be
// refused under the unnarrowed production policy. If it is accepted, the
// certificate-transparency requirement has been removed.
func TestTheProductionPolicyRefusesACertificateWithNoTransparencyProof(t *testing.T) {
	sigstore, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatalf("virtual sigstore: %v", err)
	}
	binary := []byte("signed by a Fulcio that never published the certificate")
	entity, err := sigstore.Sign(testSAN, testIssuer, binary)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	// No opts: the production policy, whole.
	if err := verifyCosignEntity(entity, sha256.Sum256(binary), sigstore, testSAN, testIssuer); err == nil {
		t.Error("a signing certificate carrying no Certificate Transparency proof was " +
			"accepted.\n\nThat proof is what stops a certificate Fulcio was tricked into " +
			"issuing from being used quietly — without it, the misissuance is never visible " +
			"to anyone watching the log.")
	}
}

// THE TEST THIS FILE WAS MISSING, and the release that proved it.
//
// v3.17.29 shipped a verifier that refused the very release carrying it. Every
// test above passed. They passed because sigstore-go's in-process CA attaches an
// RFC3161 timestamp to what it signs and `cosign sign-blob` does not: the
// harness met the observer-timestamp threshold from a source that does not exist
// in a real bundle, so the missing WithTransparencyLog clause — the one that
// makes the log's timestamp count at all — was invisible.
//
// The gap was never in the code under test. It was in the fixture, which had a
// property the product does not, and no amount of forged-input testing against
// that fixture could have found it. So the fixture here is a bundle taken
// verbatim from a published GitHub release, verified against the SHA-256 that
// release published, under the UNNARROWED production policy.
//
// It also carries THE TEN-MINUTE TRAP, which nothing else can. Keyless signing
// certificates live about ten minutes, so every release an operator installs was
// signed by a certificate that expired long before they clicked Update. A
// verifier judging that certificate against time.Now() rejects every genuine
// release; the obvious repair — skipping expiry — accepts a stolen certificate
// forever. The correct answer is neither: the log's signed record of WHEN is the
// reference point. The in-process CA cannot express this at all, because
// SignAtTime backdates the log entry while still minting the certificate now —
// which is why the test that claimed to cover it was passing on a certificate
// valid at the moment it ran.
//
// If this fails, installs cannot take the next update.
func TestThePublishedReleaseVerifiesUnderTheProductionPolicy(t *testing.T) {
	bundleJSON := publishedReleaseBundle(t)

	// The certificate must genuinely be dead by now, or the paragraph above is a
	// story rather than a test — a fixture regenerated with a live certificate
	// would pass while proving nothing.
	cert := publishedReleaseCertificate(t)
	if !cert.NotAfter.Before(time.Now()) {
		t.Fatalf("the fixture's signing certificate has not expired yet (valid until %s), "+
			"so this test cannot show that verification survives expiry", cert.NotAfter)
	}

	if err := verifyReleaseBundleDigest(publishedReleaseDigest(t), bundleJSON); err != nil {
		t.Fatalf("a real published VayuPress release was refused by the shipped policy: %v\n\n"+
			"This is not a test-harness problem. Every install running this binary would "+
			"refuse the next update — including one published to fix it. The in-process CA "+
			"cannot show this, because it attaches an RFC3161 timestamp that a real "+
			"cosign bundle does not have.", err)
	}
}

// The published bundle must be refused against any other bytes. Without this,
// the test above would still pass if the digest were ignored entirely, and
// "verified" would mean nothing more than "a genuine release exists somewhere".
func TestThePublishedReleaseBundleIsRefusedAgainstOtherBytes(t *testing.T) {
	if err := verifyReleaseBundleDigest(sha256.Sum256([]byte("the attacker's binary")), publishedReleaseBundle(t)); err == nil {
		t.Error("a genuine release bundle verified against a different artifact — the " +
			"signature would then transfer to any binary an attacker cared to publish " +
			"beside it")
	}
}

// publishedReleaseBundle is the signature bundle of a real GitHub release,
// stored verbatim.
func publishedReleaseBundle(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "published_release.cosign.bundle"))
	if err != nil {
		t.Fatalf("read the published release bundle: %v", err)
	}
	return b
}

// publishedReleaseDigest is the SHA-256 that release published beside the bundle
// above, as vayupress.sha256. Sigstore signs the digest, so the fifty-megabyte
// binary is not needed to check its signature.
func publishedReleaseDigest(t *testing.T) [sha256.Size]byte {
	t.Helper()
	raw, err := hex.DecodeString("50e1b3468ef588f3d5339a5a64f123fd70936693b04875c4114d1dbb69155fcc")
	if err != nil {
		t.Fatalf("fixture digest: %v", err)
	}
	var digest [sha256.Size]byte
	copy(digest[:], raw)
	return digest
}

// publishedReleaseCertificate is the Fulcio leaf out of that same bundle.
//
// Read from the bundle rather than kept beside it as a second PEM fixture: two
// copies of the same certificate are a divergence waiting to happen, and the
// question every test below asks is what Fulcio issued for THIS signature.
func publishedReleaseCertificate(t *testing.T) *x509.Certificate {
	t.Helper()
	var b bundle.Bundle
	if err := b.UnmarshalJSON(publishedReleaseBundle(t)); err != nil {
		t.Fatalf("parse the published release bundle: %v", err)
	}
	vc, err := b.VerificationContent()
	if err != nil {
		t.Fatalf("verification content: %v", err)
	}
	cert := vc.Certificate()
	if cert == nil {
		t.Fatal("the published release bundle carries no signing certificate")
	}
	return cert
}

// The production policy demands the signing certificate prove it was published
// to a Certificate Transparency log. That demand is only safe if real release
// certificates actually carry the proof — otherwise enforcement would refuse
// every genuine update, which is an outage wearing a security badge.
//
// Asserted against a certificate taken from a PUBLISHED release rather than one
// this test mints, because the question is what Fulcio really issues.
func TestTheReleaseCertificateCarriesTheProofThisPolicyRequires(t *testing.T) {
	cert := publishedReleaseCertificate(t)

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
	// The load-bearing precondition for reaching v3.17.29 installs at all.
	//
	// That version counts RFC3161 timestamps and never counts the log entry, so
	// the release pipeline attaches an RFC3161 countersignature to give it
	// something it can verify. That rescue works only if the timestamp authority
	// that signed it is anchored here — otherwise the countersignature is
	// unverifiable, v3.17.29 refuses the release, and nothing later reaches it
	// because its verifier is the broken one.
	if len(tm.TimestampingAuthorities()) == 0 {
		t.Error("the embedded trust root names no timestamp authority.\n\n" +
			"The RFC3161 countersignature on every release is then unverifiable, and " +
			"v3.17.29 installs — whose own verifier cannot count a log entry — are " +
			"stranded on a version no update can replace.")
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
	cert := publishedReleaseCertificate(t)

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
