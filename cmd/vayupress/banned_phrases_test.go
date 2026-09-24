// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// "Something went wrong" tells the reader nothing they can act on: not what
// failed, not whether to retry, not what to check. The console and the public
// pages say what happened instead. The scan covers every source that renders
// text for a person — the console's Go and scripts and the public renderer —
// and reports the file and line, one hit per occurrence.
var bannedPhrases = regexp.MustCompile(`(?i)something went wrong|an error (?:has )?occurred|\boops\b`)

func TestNoPageSaysSomethingWentWrong(t *testing.T) {
	var files []string
	for _, pat := range []string{"*.go", "../../static/js/*.js", "../../internal/render/*.go"} {
		m, err := filepath.Glob(pat)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, m...)
	}
	if len(files) < 50 {
		t.Fatalf("the scan found only %d files; it is not looking where the text is", len(files))
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || strings.HasSuffix(f, ".min.js") {
			continue
		}
		src, err := os.ReadFile(f) // #nosec G304 -- repository file
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			if m := bannedPhrases.FindString(line); m != "" {
				t.Errorf("%s:%d says %q; say what failed and what to do", f, i+1, m)
			}
		}
	}
}
