// SPDX-License-Identifier: Apache-2.0

package update

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// SECTION 5 AUDIT — the other end of the same control.
//
// ApplyVerified refuses a release it cannot verify. That is only safe if every
// release is actually signed, and the pipeline was explicitly built not to
// guarantee that: the cosign installer and the signing step both carried
// `continue-on-error: true`, and the signing command ended in
//
//	|| echo "cosign signing skipped/failed — publishing release without signature"
//
// so any hiccup shipped an unsigned binary. Nothing noticed, because nothing
// checked. With enforcement on, that same hiccup produces a release no install
// on earth can apply — the two ends have to move together or the product breaks
// in the field.
//
// This reads the workflow file because the workflow IS the artifact; there is
// nothing to render and no behaviour to drive. It is the one case where
// asserting on file text is the only honest option, so the assertions are kept
// narrow and each says what would go wrong.

func releaseWorkflow(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "tag-release.yml"))
	if err != nil {
		t.Fatalf("read the release workflow: %v", err)
	}
	return string(b)
}

// signBinaryStep returns just the step that signs the release binary.
//
// Extracted rather than searched for across the whole file: this workflow signs
// four different artifacts, three of them still best-effort, so a check that
// scanned the file as a whole would report on whichever step it happened to
// match and pass or fail for the wrong reason.
func signBinaryStep(t *testing.T, wf string) string {
	t.Helper()
	const marker = "- name: Sign binary (keyless OIDC)"
	i := strings.Index(wf, marker)
	if i < 0 {
		t.Fatal("the release workflow no longer has a step named \"Sign binary (keyless OIDC)\".\n\n" +
			"The updater refuses any release without a signature, so if signing has been " +
			"renamed or removed, every install has stopped updating.")
	}
	rest := wf[i+len(marker):]
	// The step ends at the next step at the same indentation.
	if j := regexp.MustCompile(`\n      - name:`).FindStringIndex(rest); j != nil {
		rest = rest[:j[0]]
	}
	return stripYAMLComments(rest)
}

// stripYAMLComments removes comment lines so an assertion cannot be satisfied by
// prose describing the thing it is checking for.
//
// Found by mutation, not by inspection: removing --new-bundle-format from the
// cosign command left the test passing, because the comment ABOVE that command
// explains why the flag is there and contains the flag. The gate was reading its
// own documentation. Every assertion below now sees only what the runner
// executes.
func stripYAMLComments(step string) string {
	var kept []string
	for _, ln := range strings.Split(step, "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), "#") {
			continue
		}
		kept = append(kept, ln)
	}
	return strings.Join(kept, "\n")
}

func TestTheReleasePipelineCannotPublishAnUnsignedBinary(t *testing.T) {
	step := signBinaryStep(t, releaseWorkflow(t))

	if strings.Contains(step, "continue-on-error") {
		t.Error("the binary signing step is marked continue-on-error, so a release " +
			"whose signing failed is still published.\n\n" +
			"The updater refuses an unverifiable release, so that build reaches every " +
			"operator as a broken update rather than as a failed release.")
	}
	if strings.Contains(step, "||") {
		t.Error("the binary signing command swallows its own failure with a `||` " +
			"fallback.\n\nThat is how the pipeline came to publish unsigned releases " +
			"nobody noticed: the step reported success and the artifact went out anyway.")
	}
	// The updater reads the protobuf bundle; the legacy default shape is one it
	// cannot parse, so a release signed without this flag is unverifiable.
	if !strings.Contains(step, "--new-bundle-format") {
		t.Error("the binary is signed without --new-bundle-format, which emits the " +
			"legacy bundle shape the updater cannot read. Every release would be " +
			"refused as unverifiable.")
	}
	// An RFC3161 countersignature is a second independent observer of when the
	// signature was made — and, concretely, the only route by which a v3.17.29
	// install can take an update at all. That version's policy counts RFC3161
	// timestamps and, through the bug v3.17.30 fixes, never counts the log entry,
	// so a bundle carrying only the log entry is refused by exactly the installs
	// that need the fix. Dropping this flag strands them permanently.
	if !strings.Contains(step, "--timestamp-server-url") {
		t.Error("the binary is signed with no RFC3161 timestamp.\n\n" +
			"Every v3.17.29 install would refuse this release — and v3.17.29 is the " +
			"version whose verifier is broken, so there is no later release that reaches " +
			"them either. They would need the binary replaced by hand.")
	}
}

// The signed artifact and the asset the updater looks for must be the same file.
// A rename on either side leaves the release signed and the updater unable to
// find the signature, which fails exactly as loudly as an attack.
func TestTheSignedBundleIsNamedWhatTheUpdaterLooksFor(t *testing.T) {
	step := signBinaryStep(t, releaseWorkflow(t))

	// What ApplyVerified will ask for, derived rather than restated.
	want := *selectBundleAsset([]Asset{{Name: "vayupress.cosign.bundle"}}, "vayupress")
	if !strings.Contains(step, "dist/"+want.Name) {
		t.Errorf("the workflow does not write dist/%s, which is the asset name the "+
			"updater resolves for a binary called vayupress.\n\nThe release would be "+
			"signed and still refused.\n\nstep:\n%s", want.Name, step)
	}
}

// The installer must be fatal too. If cosign is missing the signing step fails
// anyway, but a green installer step that quietly did nothing sends whoever
// debugs it to the wrong place.
func TestTheCosignInstallerIsNotBestEffort(t *testing.T) {
	wf := releaseWorkflow(t)
	i := strings.Index(wf, "- name: Install cosign")
	if i < 0 {
		t.Fatal("no cosign installer step in the release workflow")
	}
	rest := wf[i:]
	if j := regexp.MustCompile(`\n      - name:`).FindStringIndex(rest); j != nil {
		rest = rest[:j[0]]
	}
	if strings.Contains(stripYAMLComments(rest), "continue-on-error") {
		t.Error("the cosign installer is best-effort, so a failed install surfaces later " +
			"as a signing error rather than where it happened")
	}
}
