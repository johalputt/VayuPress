// Command frontmatter-guard is an example VayuPress sandbox plugin demonstrating
// a pure GOVERNANCE CHECK with zero capabilities.
//
// It validates an article payload against a few editorial invariants — a
// non-empty title, a slug that looks like a slug, and content over a minimum
// length — and signals a governance failure by returning {"ok": false, ...}.
// The host records hook failures and, after a threshold, quarantines the hook;
// so a guard like this turns an editorial rule into an observable governance
// signal without any side effects.
//
// It declares NO filesystem, network, or write access — the strictest possible
// sandbox, appropriate for a stateless validator.
//
// Build:
//
//	go build -o frontmatter-guard ./docs/plugins/examples/frontmatter-guard
//
// Register (host side):
//
//	m := sandbox.Manifest{
//	    Name:       "frontmatter-guard",
//	    Executable: "/opt/vayupress/plugins/frontmatter-guard",
//	    Timeout:    2 * time.Second,
//	    // Fully isolated: no AllowedReadPaths / AllowedWritePaths / AllowNetwork.
//	}
//	plugins.RegisterSubprocess(reg, m, "article.created.v1", 2)
package main

import (
	"bufio"
	"encoding/json"
	"os"
	"regexp"
	"strings"
)

type request struct {
	Hook    string                 `json:"hook"`
	Payload map[string]interface{} `json:"payload"`
}

type logLine struct {
	Level   string `json:"level"`
	Message string `json:"msg"`
}
type response struct {
	OK       bool      `json:"ok"`
	Error    string    `json:"error,omitempty"`
	LogLines []logLine `json:"log_lines,omitempty"`
}

// slugRe is the editorial slug invariant: lowercase, digits, single hyphens.
var slugRe = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

const minContentLen = 50 // characters; reject near-empty drafts going public

// violations returns the list of editorial invariants the payload breaks.
func violations(p map[string]interface{}) []string {
	var out []string
	title, _ := p["title"].(string)
	slug, _ := p["slug"].(string)
	content, _ := p["content"].(string)

	if strings.TrimSpace(title) == "" {
		out = append(out, "title is empty")
	}
	if !slugRe.MatchString(slug) {
		out = append(out, "slug is not a clean lowercase-hyphen slug")
	}
	if len(strings.TrimSpace(content)) < minContentLen {
		out = append(out, "content is shorter than the minimum length")
	}
	return out
}

func main() {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	enc := json.NewEncoder(os.Stdout)

	for sc.Scan() {
		var req request
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			_ = enc.Encode(response{OK: false, Error: "bad request: " + err.Error()})
			continue
		}
		if bad := violations(req.Payload); len(bad) > 0 {
			_ = enc.Encode(response{
				OK:    false,
				Error: "frontmatter governance failure: " + strings.Join(bad, "; "),
			})
			continue
		}
		slug, _ := req.Payload["slug"].(string)
		_ = enc.Encode(response{
			OK:       true,
			LogLines: []logLine{{Level: "info", Message: "frontmatter-guard: ok slug=" + slug}},
		})
	}
}
