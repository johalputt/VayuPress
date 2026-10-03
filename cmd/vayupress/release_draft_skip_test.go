// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The release workflow's "Skip if release already exists" step, run from the
// workflow file itself against a stand-in gh. 3.17.98's publish lost its
// connection after the draft was made and before it was published; the step
// read the draft as a release and every re-run skipped, so the version could
// not be published at all. A draft is deleted and the release built; only a
// published release is skipped.
func TestTheReleaseSkipStepRebuildsAnInterruptedDraft(t *testing.T) {
	wf, err := os.ReadFile("../../.github/workflows/tag-release.yml")
	if err != nil {
		t.Fatal(err)
	}
	src := string(wf)
	start := strings.Index(src, "- name: Skip if release already exists")
	if start < 0 {
		t.Fatal("the workflow has no skip step")
	}
	body := src[start:]
	run := strings.Index(body, "run: |\n")
	end := strings.Index(body, "\n\n")
	if run < 0 || end < run {
		t.Fatal("the skip step's script was not found")
	}
	var script strings.Builder
	for _, line := range strings.Split(body[run+len("run: |\n"):end], "\n") {
		script.WriteString(strings.TrimPrefix(line, "          ") + "\n")
	}
	shell := strings.ReplaceAll(script.String(), "${{ steps.ver.outputs.version }}", "v9.9.9")

	for _, c := range []struct {
		name, state, skip string
		deleted           bool
	}{
		{"no release", "none", "skip=false", false},
		{"interrupted draft", "true", "skip=false", true},
		{"published", "false", "skip=true", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			log := filepath.Join(dir, "gh.log")
			gh := "#!/bin/sh\necho \"$@\" >> " + log + "\n" +
				"case \"$1 $2\" in\n" +
				"  'release view') [ \"" + c.state + "\" = none ] && exit 1; echo " + c.state + ";;\n" +
				"esac\n"
			if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(gh), 0o755); err != nil { // #nosec G306 -- an executable test stand-in
				t.Fatal(err)
			}
			out := filepath.Join(dir, "output")
			cmd := exec.Command("bash", "-e", "-c", shell) // #nosec G204 -- the workflow's own step
			cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "GITHUB_OUTPUT="+out)
			if b, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("the step failed: %v\n%s", err, b)
			}
			got, _ := os.ReadFile(out)
			if strings.TrimSpace(string(got)) != c.skip {
				t.Errorf("the step decided %q, want %q", strings.TrimSpace(string(got)), c.skip)
			}
			calls, _ := os.ReadFile(log)
			if deleted := strings.Contains(string(calls), "release delete v9.9.9 --yes"); deleted != c.deleted {
				t.Errorf("deleted the release: %v, want %v (calls: %s)", deleted, c.deleted, calls)
			}
		})
	}
}
