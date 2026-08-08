// SPDX-License-Identifier: Apache-2.0

package update

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SECTION 6 AUDIT — the rule the binary learned, that the root scripts did not.
//
// cosign.go says it in its own words, about the release binary:
//
//	The branch is part of the identity on purpose. A workflow file of the same
//	name on an attacker's branch, or in a fork, produces a perfectly valid
//	Sigstore signature carrying a DIFFERENT SAN — and would be accepted by any
//	policy lazy enough to match on the repository alone.
//
// Three scripts then verified code they install and execute AS ROOT with exactly
// the policy that paragraph rules out:
//
//	--certificate-identity-regexp "^https://github.com/${UPGRADE_REPO}/"
//
// Anchored at the front, open at the back. Every one of these matches:
//
//	…/VayuPress/.github/workflows/tag-release.yml@refs/heads/main   (intended)
//	…/VayuPress/.github/workflows/anything.yml@refs/heads/anybranch
//	…/VayuPress/.github/workflows/ci.yml@refs/pull/1/merge
//
// In the attacker's voice: I do not need your release key or your main branch. I
// need one workflow, on one branch, in this repository that can mint an OIDC
// token — then I sign a tarball of shell scripts, and every install's root
// worker downloads it, verifies it happily, and runs `install -o root` on my
// files. The binary would have refused the same certificate.
//
// The comment above one of those three sites read "The signer is pinned to this
// project, because accepting any valid signature accepts an attacker's." That is
// true of the repository and false of the workflow and the branch, which is the
// half that matters — a claim describing a control stricter than the one below it.
//
// The expected identity is DERIVED from ReleaseSignerIdentity rather than
// restated, so a script and the binary cannot drift apart: they are pinned to
// one string by construction. The repository segment is the scripts' own
// variable, because a fork that publishes its own signed helpers is a supported
// deployment and must keep working.
func TestRootExecutedBundlesArePinnedToTheWorkflowAndBranch(t *testing.T) {
	// "/.github/workflows/tag-release.yml@refs/heads/main" — everything after
	// the repository, taken from the constant the updater enforces.
	const repoPrefix = "https://github.com/johalputt/VayuPress"
	suffix, ok := strings.CutPrefix(ReleaseSignerIdentity, repoPrefix)
	if !ok {
		t.Fatalf("ReleaseSignerIdentity (%q) no longer starts with %q, so this gate cannot "+
			"derive what the scripts must pin", ReleaseSignerIdentity, repoPrefix)
	}

	checked := 0
	for _, rel := range []string{
		filepath.Join("..", "..", "scripts", "provision-subdomains.sh"),
		filepath.Join("..", "..", "deploy", "vayushield-agent.sh"),
	} {
		b, err := os.ReadFile(rel)
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if !strings.Contains(line, "--certificate-identity") {
				continue
			}
			checked++
			if strings.Contains(line, "--certificate-identity-regexp") {
				t.Errorf("%s:%d verifies root-executed code with an identity REGEXP:\n  %s\n\n"+
					"Anchored at the front and open at the back, it accepts any workflow on any "+
					"branch or pull request of the repository. Every one of them signs code this "+
					"script installs with `install -o root` and then executes.",
					rel, i+1, line)
				continue
			}
			if !strings.Contains(line, suffix) {
				t.Errorf("%s:%d pins an identity that does not name the release workflow and "+
					"branch:\n  %s\n\nExpected it to end in %q, matching ReleaseSignerIdentity.",
					rel, i+1, line, suffix)
			}
		}
	}
	// A gate that silently checked nothing would pass for ever.
	if checked < 3 {
		t.Errorf("found %d cosign identity policies in the root helpers; expected at least 3.\n\n"+
			"Either a verification was removed — in which case root now installs unverified "+
			"code — or this gate has stopped finding them.", checked)
	}
}

// The pin must match what the pipeline REALLY signs, or the scripts refuse
// every genuine helper bundle and quietly stop upgrading — the mirror image of
// the v3.17.29 mistake, in a place with no telemetry at all, since a skip here
// is a log line on a machine nobody is watching.
//
// Asserted against the certificate from a PUBLISHED release rather than one
// this test reasons about. The cosign CLI cannot verify it here (the sandbox
// proxy refuses Sigstore's TUF CDN), so this checks the half this change
// touched — the identity — and makes no claim about the signature or chain,
// which this change did not alter.
func TestThePinnedIdentityIsWhatThePipelineActuallySigns(t *testing.T) {
	pemBytes, err := os.ReadFile(filepath.Join("testdata", "published_root_helper_cert.pem"))
	if err != nil {
		t.Fatalf("read the published helper certificate: %v", err)
	}
	blk, _ := pem.Decode(pemBytes)
	if blk == nil {
		t.Fatal("fixture is not PEM")
	}
	cert, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(cert.URIs) != 1 || cert.URIs[0].String() != ReleaseSignerIdentity {
		t.Errorf("the helper bundle root installs and executes is signed as %v, but the\n"+
			"scripts now pin %q.\n\nEvery genuine upgrade would be refused, and the only trace\n"+
			"is a skip line in a log on the server.", cert.URIs, ReleaseSignerIdentity)
	}
}
