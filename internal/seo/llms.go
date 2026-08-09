// SPDX-License-Identifier: Apache-2.0

package seo

import (
	"strings"
	"time"
	"unicode/utf8"
)

// llms.go — /llms.txt, the plain-text map a language model reads.
//
// WHAT IT IS. robots.txt tells a crawler what it may fetch; a sitemap tells it
// which URLs exist. Neither tells a model what the site is ABOUT, and a model
// answering a question has neither the budget nor the permission to crawl a
// whole archive to find out. /llms.txt (llmstxt.org) is the emerging convention
// for that gap: one small Markdown file, at a fixed path, that names the site,
// says what it publishes, and lists its content as links with one-line
// summaries.
//
// WHY IT IS WORTH HAVING. Generative engines increasingly answer from a handful
// of fetched pages rather than a ranked list. A curated index makes the
// difference between being summarised from whatever page happened to rank and
// being summarised from the page the author would have chosen — and the summary
// is what a reader now sees instead of the site.
//
// WHAT IT IS NOT. It is not an access control and it is not a licence. It does
// not stop anybody training on anything, and this file does not pretend it does.
// The switch that actually governs crawling is the operator's crawler block,
// which the handler consults before this is ever rendered — an llms.txt served
// while robots.txt says disallow-everything would be a published invitation
// contradicting a stated refusal.

// LLMsDoc is the input for Render.
type LLMsDoc struct {
	SiteName    string
	Origin      string // "https://example.com", no trailing slash
	Tagline     string
	Description string
	Posts       []LLMsPost
	// Generated is stamped into the file so a consumer can tell a fresh copy
	// from one a cache has been holding for a month.
	Generated time.Time
}

// LLMsPost is one linked entry.
type LLMsPost struct {
	Title     string
	URL       string // absolute
	Summary   string
	Published time.Time
}

// llmsSummaryMax keeps each line to roughly one sentence. The format's value is
// that the whole file fits comfortably in a context window beside the question
// being asked; pasting full posts in defeats it, and /llms-full.txt is the
// convention's answer for that, not this file.
const llmsSummaryMax = 200

// Render returns the body of /llms.txt, or "" when there is nothing truthful to
// say (no origin).
func Render(d LLMsDoc) string {
	origin := strings.TrimRight(d.Origin, "/")
	if origin == "" {
		return ""
	}
	name := d.SiteName
	if name == "" {
		name = strings.TrimPrefix(strings.TrimPrefix(origin, "https://"), "http://")
	}

	var b strings.Builder
	b.WriteString("# " + oneLine(name) + "\n\n")

	if s := oneLine(firstNonEmpty(d.Description, d.Tagline)); s != "" {
		b.WriteString("> " + s + "\n\n")
	}

	b.WriteString("- Site: " + origin + "/\n")
	b.WriteString("- Feed: " + origin + "/feed.xml\n")
	b.WriteString("- Sitemap: " + origin + "/sitemap.xml\n")
	if !d.Generated.IsZero() {
		b.WriteString("- Generated: " + d.Generated.UTC().Format(time.RFC3339) + "\n")
	}
	b.WriteString("\n")

	if len(d.Posts) == 0 {
		// Saying so beats an empty heading that reads like a truncated file.
		b.WriteString("No posts have been published yet.\n")
		return b.String()
	}

	b.WriteString("## Posts\n\n")
	for _, p := range d.Posts {
		if p.URL == "" || p.Title == "" {
			continue
		}
		b.WriteString("- [" + oneLine(p.Title) + "](" + p.URL + ")")
		if s := clip(oneLine(p.Summary), llmsSummaryMax); s != "" {
			b.WriteString(": " + s)
		}
		if !p.Published.IsZero() {
			b.WriteString(" (" + p.Published.UTC().Format("2006-01-02") + ")")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// oneLine flattens whitespace. A newline inside a title would otherwise break
// the list item it sits in and silently swallow the rest of the entry — the
// format is line-oriented, so this is a correctness step, not tidying.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// Cut at a rune boundary, then at the last space, so a clipped summary does
	// not end mid-word or mid-character.
	//
	// The first version of this tested the last byte with `b&0xC0 != 0x80` and
	// stopped when it was NOT a continuation byte — which stops on the LEAD byte
	// of a multi-byte rune and leaves that rune truncated. It survived review and
	// a test, because the word-boundary cut below happens to land on a valid
	// boundary for any string containing spaces. A Devanagari summary with no
	// spaces produced invalid UTF-8.
	cut := s[:n]
	for len(cut) > 0 {
		if r, size := utf8.DecodeLastRuneInString(cut); r != utf8.RuneError || size > 1 {
			break
		}
		cut = cut[:len(cut)-1]
	}
	if i := strings.LastIndex(cut, " "); i > n/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,;:") + "…"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
