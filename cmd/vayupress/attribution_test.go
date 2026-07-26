package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The repository has one standing rule about attribution: nothing pushed to it
// credits an AI assistant. Commit messages, code comments, changelog entries, PR
// bodies and shipped copy carry the maintainer's identity and nothing else.
//
// Enforcing that by remembering does not work. The leak that prompted this guard
// was a single `CLAUDE.md §4` cross-reference in a code comment — harmless
// looking, and then copied outward into a public pull-request reply where it
// long outlived anyone's memory of writing it. So the build enforces it.
//
// The distinction that makes this test useful rather than annoying: VayuPress
// LEGITIMATELY names these companies and products. VayuMCP connects from
// claude.ai, VayuShield's crawler database classifies ChatGPT-User and
// ClaudeBot, and the AI editor offers `anthropic/claude-3.5-sonnet` as a model.
// Banning the words would fail the build on correct product copy and the guard
// would be deleted within a week. So this matches ATTRIBUTION SHAPES only —
// strings that can only mean "an assistant wrote this".

// attributionPatterns are the shapes that mean authorship, not subject matter.
var attributionPatterns = []struct {
	name string
	re   *regexp.Regexp
}{
	// The assistant config file. Referencing it by name in a pushed artifact
	// exports the workflow into public view — the original leak.
	{"a reference to the assistant config file", regexp.MustCompile(`(?i)\bCLAUDE\.md\b`)},
	{"an AI co-author trailer", regexp.MustCompile(`(?i)co-authored-by:\s*(claude|ai\b|assistant)`)},
	{"a generated-with credit", regexp.MustCompile(`(?i)generated with \[?(claude|chatgpt|copilot|ai\b)`)},
	{"an assistant session link", regexp.MustCompile(`(?i)claude\.ai/code|claude-session:`)},
	{"a robot authorship emoji credit", regexp.MustCompile(`🤖\s*Generated`)},
	// Deliberately NOT matching a bare "AI-generated": ADR-0114 uses it to
	// describe the operator's own website content ("hand-written or
	// AI-generated"), which is product copy, not a claim about who wrote this
	// repository. A pattern that fires there would be switched off within a week.
}

// attributionExempt are paths where a match is expected. Keep this list short:
// every entry is a place the rule does not reach.
var attributionExempt = map[string]bool{
	"CLAUDE.md":                         true, // the config itself; its own name is unavoidable
	"cmd/vayupress/attribution_test.go": true, // this file has to spell out what it forbids
}

// attributionScanExt limits the sweep to what actually gets published or read by
// a contributor.
var attributionScanExt = map[string]bool{
	".go": true, ".md": true, ".yml": true, ".yaml": true,
	".html": true, ".css": true, ".js": true, ".sh": true, ".sql": true,
}

// TestNoAssistantAttributionInTrackedFiles sweeps every tracked source and
// documentation file.
func TestNoAssistantAttributionInTrackedFiles(t *testing.T) {
	root := filepath.Join("..", "..")
	var offences []string

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // unreadable entries are not this test's business
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)

		if info.IsDir() {
			switch info.Name() {
			case ".git", "node_modules", "vendor", "dist", ".claude":
				return filepath.SkipDir
			}
			return nil
		}
		if attributionExempt[rel] || !attributionScanExt[strings.ToLower(filepath.Ext(rel))] {
			return nil
		}
		if info.Size() > 4<<20 { // a generated bundle, not prose
			return nil
		}
		b, readErr := os.ReadFile(path) // #nosec G304 -- walking this repository
		if readErr != nil {
			return nil
		}
		body := string(b)
		for _, p := range attributionPatterns {
			loc := p.re.FindStringIndex(body)
			if loc == nil {
				continue
			}
			line := 1 + strings.Count(body[:loc[0]], "\n")
			offences = append(offences, rel+":"+itoa(line)+" — "+p.name+
				" ("+strings.TrimSpace(body[loc[0]:loc[1]])+")")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	for _, o := range offences {
		t.Errorf("assistant attribution in a pushed artifact: %s", o)
	}
	if len(offences) > 0 {
		t.Log("Pushed artifacts carry the maintainer's identity only. " +
			"Cite an ADR or describe the contributor notes; do not name the config file.")
	}
}

// TestAttributionGuardAllowsLegitimateProductCopy is the other half. A guard that
// fires on correct content gets deleted, so this pins that the real product
// surfaces — the MCP connector, the crawler database, the AI model picker — stay
// describable.
func TestAttributionGuardAllowsLegitimateProductCopy(t *testing.T) {
	for _, ok := range []string{
		"one-click Connect from claude.ai for Claude and Claude Code",
		"ClaudeBot and ChatGPT-User are classified as AI crawlers",
		`"anthropic/claude-3.5-sonnet", "openai/gpt-4o"`,
		"Anthropic's OAuth backend cannot solve a browser challenge",
		"the user-facing name is VayuMCP, not the Claude Connector",
	} {
		for _, p := range attributionPatterns {
			if p.re.MatchString(ok) {
				t.Errorf("guard fires on legitimate product copy (%s): %q", p.name, ok)
			}
		}
	}
}

// TestAttributionGuardCatchesRealLeaks proves the patterns bite. Without this the
// guard could be silently defanged by a bad regex edit and would keep passing.
func TestAttributionGuardCatchesRealLeaks(t *testing.T) {
	for _, bad := range []string{
		"Worth running the gates in `CLAUDE.md` §4 locally",
		"Co-Authored-By: Claude <noreply@anthropic.com>",
		"🤖 Generated with [Claude Code](https://claude.com/claude-code)",
		"https://claude.ai/code/session_abc123",
		"see CLAUDE.md §8 for the Tor rules",
	} {
		var hit bool
		for _, p := range attributionPatterns {
			if p.re.MatchString(bad) {
				hit = true
				break
			}
		}
		if !hit {
			t.Errorf("guard misses a real attribution leak: %q", bad)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
