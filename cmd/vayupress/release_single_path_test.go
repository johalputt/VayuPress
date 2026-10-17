// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// tag-release.yml is the one workflow that may publish a GitHub release. Every
// install refuses a release without the release key's signature, so a second
// publisher that does not make one — release.yml on a hand-pushed tag, and
// publish-v1.9.0.yml on a dispatch with signing "best-effort" did not — ships a
// release no install can take, and tag-release's skip-if-exists then leaves it
// standing. Publishing is matched by what does it, not by a workflow's name.
func TestOnlyTagReleasePublishesReleases(t *testing.T) {
	publishes := regexp.MustCompile(`softprops/action-gh-release|gh release (create|upload|edit|delete)`)
	files, err := filepath.Glob("../../.github/workflows/*.y*ml")
	if err != nil || len(files) == 0 {
		t.Fatalf("no workflows found: %v", err)
	}
	for _, f := range files {
		b, err := os.ReadFile(f) // #nosec G304 -- repository file
		if err != nil {
			t.Fatal(err)
		}
		if filepath.Base(f) != "tag-release.yml" && publishes.Match(b) {
			t.Errorf("%s publishes a GitHub release; only tag-release.yml may, because only it "+
				"makes the release-key signature every install requires", filepath.Base(f))
		}
	}
}
