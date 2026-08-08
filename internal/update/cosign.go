// SPDX-License-Identifier: Apache-2.0

package update

// cosign.go — authenticity for the self-update path (Section 5 audit).
//
// Before this, the only thing standing between a release and every install was a
// SHA-256 checksum published by that same release and fetched over the same
// connection. That proves the bytes survived the network. It says nothing about
// who produced them, and docs/SECURITY.md said so itself while the shipped
// default did exactly the thing it argued against.
//
// Every release has in fact been Sigstore-signed the whole time; nothing ever
// looked at the signature. This reads it.
//
// WHAT THE POLICY ACTUALLY ASSERTS, because "signature verified" is the kind of
// phrase that hides the interesting part: a Sigstore signature proves that
// SOMEBODY held a Fulcio certificate and signed these bytes. Anyone can run a
// GitHub Action and obtain one. The security therefore lives entirely in the
// identity policy below — the signature is only meaningful pinned to the one
// workflow permitted to publish a VayuPress release.

import (
	"crypto/sha256"
	_ "embed"
	"errors"
	"fmt"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

// The release signing identity, read off the certificate in a published bundle
// rather than assumed. `openssl x509 -text` on the cert inside
// vayupress.cosign.bundle shows exactly this SAN and issuer.
//
// The branch is part of the identity on purpose. A workflow file of the same
// name on an attacker's branch, or in a fork, produces a perfectly valid
// Sigstore signature carrying a DIFFERENT SAN — and would be accepted by any
// policy lazy enough to match on the repository alone.
const (
	ReleaseSignerIdentity = "https://github.com/johalputt/VayuPress/.github/workflows/tag-release.yml@refs/heads/main"
	ReleaseSignerIssuer   = "https://token.actions.githubusercontent.com"
)

// ErrNoTrustRoot reports that verification could not run because the pinned
// Sigstore trust root is unusable. It is deliberately distinct from "the
// signature is bad": one means an attacker, the other means this binary needs
// replacing by hand, and an operator must be able to tell those apart.
var ErrNoTrustRoot = errors.New("update: the pinned Sigstore trust root is unusable")

// sigstoreTrustedRoot is the Sigstore public-good trust root, embedded so
// verification needs NO network.
//
// Fetching it over TUF at update time would be the conventional choice and is
// wrong here for two reasons. In Tor mode every clearnet callback is refused by
// design, so a fetch would make updates impossible in the one mode where
// unexplained outbound traffic is the whole threat. And an update path that
// silently degrades when a fetch fails is precisely the failure this section
// exists to remove.
//
//go:embed sigstore_trusted_root.json
var sigstoreTrustedRoot []byte

// ReleaseTrustMaterial returns the trust root releases are verified against.
func ReleaseTrustMaterial() (root.TrustedMaterial, error) {
	tr, err := root.NewTrustedRootFromJSON(sigstoreTrustedRoot)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoTrustRoot, err)
	}
	return tr, nil
}

// releaseVerifyOptions is the policy a real release is held to.
//
// WithObserverTimestamps is what makes verification possible at all, and is
// explained at the call site. WithSignedCertificateTimestamps additionally
// requires the signing certificate to carry proof that it was published to a
// Certificate Transparency log — so a certificate Fulcio was tricked into
// issuing cannot be used quietly. Checked against a real release certificate
// rather than assumed: the one decoded from a published bundle carries the
// "CT Precertificate SCTs" extension, which is why requiring it is safe.
//
// COVERAGE NOTE, stated because a silent gap is worse than a named one:
// sigstore-go's in-process test CA does not mint SCTs, so the tests below drive
// every other clause of this policy and NOT this one. TestTheReleaseCertificate
// CarriesTheProofThisPolicyRequires covers it against the real certificate
// instead.
func releaseVerifyOptions() []verify.VerifierOption {
	return []verify.VerifierOption{
		verify.WithSignedCertificateTimestamps(1),
		verify.WithObserverTimestamps(1),
	}
}

// verifyCosignEntity checks one parsed Sigstore entity against artifact, the
// trust material, and the signer identity. opts defaults to the production
// policy; the tests narrow it only where the in-process CA cannot reach.
//
// Split from bundle parsing so the tests can drive it with an in-process CA and
// forged input: there is no way to obtain a genuinely forged Sigstore bundle, so
// a verifier only ever exercised against real releases is a verifier nobody has
// ever seen say no.
func verifyCosignEntity(entity verify.SignedEntity, artifact []byte, tm root.TrustedMaterial, identity, issuer string, opts ...verify.VerifierOption) error {
	certID, err := verify.NewShortCertificateIdentity(issuer, "", identity, "")
	if err != nil {
		return fmt.Errorf("update: build signer identity policy: %w", err)
	}
	if len(opts) == 0 {
		opts = releaseVerifyOptions()
	}

	// WithObserverTimestamps is what makes this work at all, and it is worth
	// stating why rather than leaving it as an incantation.
	//
	// A keyless signing certificate is valid for about ten minutes. Every release
	// an operator installs was signed by a certificate that expired long before
	// they clicked Update. Verifying it against the current time would therefore
	// reject every genuine release, and the obvious repair — skipping expiry —
	// would accept a stolen certificate forever. Requiring an observer timestamp
	// makes the transparency log's own signed record of WHEN the signature was
	// made the reference point, and the certificate is judged as of that instant.
	v, err := verify.NewVerifier(tm, opts...)
	if err != nil {
		return fmt.Errorf("update: build verifier: %w", err)
	}

	digest := sha256.Sum256(artifact)
	if _, err := v.Verify(entity, verify.NewPolicy(
		verify.WithArtifactDigest("sha256", digest[:]),
		verify.WithCertificateIdentity(certID),
	)); err != nil {
		return fmt.Errorf("update: release signature rejected: %w", err)
	}
	return nil
}

// VerifyReleaseBundle is the production entry point: it checks that bundleJSON
// is a Sigstore bundle proving this project's release workflow signed artifact.
//
// Enforced, with no opt-out flag. The previous design made the signature check
// conditional on an operator pinning a key, which meant the default install had
// no authenticity control and the one operator who followed the security
// documentation had a broken updater. A control that is off unless someone
// opts in protects the people who already knew, and nobody else.
func VerifyReleaseBundle(artifact, bundleJSON []byte) error {
	tm, err := ReleaseTrustMaterial()
	if err != nil {
		return err
	}
	var b bundle.Bundle
	if err := b.UnmarshalJSON(bundleJSON); err != nil {
		return fmt.Errorf("update: the release signature bundle could not be read: %w", err)
	}
	return verifyCosignEntity(&b, artifact, tm, ReleaseSignerIdentity, ReleaseSignerIssuer)
}
