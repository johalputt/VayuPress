// SPDX-License-Identifier: Apache-2.0

package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// planNames are the ways the planning that produced a piece of work has been
// named in this tree: a pipeline row by its id, a plan's
// section or step, a design render. A comment, test or stylesheet says what
// the code does and why, never which piece of planned work made it: the plans
// are kept apart from the product. Built from parts so this file does not
// match itself.
var planNames = regexp.MustCompile(strings.Join([]string{
	`\bpipeline ` + `3[a-z]{1,2}\b`,
	`\((?:pipeline |row )?` + `3[a-h][a-z](?:, ?3[a-h][a-z])*\)`,
	`\b(?:Mail |fidelity )?plan ` + `§`,
	`\bfidelity ` + `(?:plan|step)\b`,
	`\brender ` + `0[0-9]\b`,
	`\(row ` + `[0-9]+`,
}, "|"))

// No plan names in the product tree: its Go, scripts, stylesheets, specs and
// workflows. On 2026-11-20 some 300 of them were taken out, written before
// the rule or past it.
func TestNoPlanNamesInTheTree(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"cmd", "internal", "static", "tests", "scripts", ".github"} {
		_ = filepath.WalkDir(filepath.Join(root, dir), func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				if d != nil && d.IsDir() && (d.Name() == "node_modules" || d.Name() == "testdata") {
					return filepath.SkipDir
				}
				return nil
			}
			switch filepath.Ext(p) {
			case ".go", ".js", ".css", ".sh", ".yml", ".yaml":
			default:
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return nil
			}
			for n, line := range strings.Split(string(b), "\n") {
				if m := planNames.FindString(line); m != "" {
					rel, _ := filepath.Rel(root, p)
					t.Errorf("%s:%d names a plan (%q); say what the code does instead", rel, n+1, m)
				}
			}
			return nil
		})
	}
}
