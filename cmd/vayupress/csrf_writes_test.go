// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every write a console script sends carries the CSRF token. A signed-in
// operator is a cookie session, and the console refuses a cookie-session POST
// without X-CSRF-Token. Theme Studio's palette generator, contrast fix and
// draft autosave posted without it for their whole life and failed for every
// operator. Nothing noticed, because the e2e harness signs in with an API key,
// and an API-key request is exempt. So this is checked on the source, where
// the omission is visible, rather than in a browser that cannot see it.
//
// The member portal and the public analytics beacon are not console routes:
// they are exempt by path.
func TestConsoleWritesCarryTheCSRFToken(t *testing.T) {
	files, _ := filepath.Glob("../../static/js/*.js")
	gofiles, _ := filepath.Glob("*.go")
	files = append(files, gofiles...)
	write := regexp.MustCompile(`method:\s*['"](POST|PUT|PATCH|DELETE)['"]`)
	headerVar := regexp.MustCompile(`headers:\s*([A-Za-z_]\w*)\s*[,}]`)
	checked := 0
	for _, f := range files {
		if strings.HasSuffix(f, ".min.js") || strings.HasSuffix(f, "_test.go") ||
			strings.HasPrefix(filepath.Base(f), "handlers_member_") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		for i := strings.Index(src, "fetch("); i >= 0; {
			call := src[i:]
			// The call's own arguments: up to where its promise chain starts.
			if end := strings.Index(call, ".then"); end > 0 && end < 900 {
				call = call[:end]
			} else if len(call) > 900 {
				call = call[:900]
			}
			if write.MatchString(call) && !strings.Contains(call, "/__vayuanalytics/") {
				checked++
				// Headers built into a variable just before the call count when
				// that variable is where the token went.
				carried := strings.Contains(call, "X-CSRF-Token") || strings.Contains(call, "csrf")
				if m := headerVar.FindStringSubmatch(call); !carried && m != nil {
					before := src[max(0, i-400):i]
					carried = regexp.MustCompile(`var\s+` + m[1] + `\s*=\s*\{[^}]*X-CSRF-Token`).MatchString(before)
				}
				if !carried {
					line := strings.Count(src[:i], "\n") + 1
					shown := strings.Join(strings.Fields(call), " ")
					t.Errorf("%s:%d sends a write without the CSRF token, so a signed-in operator is refused:\n  %s",
						filepath.Base(f), line, shown[:min(len(shown), 120)])
				}
			}
			next := strings.Index(src[i+6:], "fetch(")
			if next < 0 {
				break
			}
			i += 6 + next
		}
	}
	if checked < 20 {
		t.Fatalf("only %d console writes found; the scan no longer sees the scripts", checked)
	}
}
