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
	Author      string
	// Pinned are the posts the operator chose to put first, the closest thing
	// the site has to "read these first", which is what the format is for.
	Pinned []LLMsPost
	// Topics are the topic pages the site offers to search engines.
	Topics []LLMsLink
	Posts  []LLMsPost
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

// LLMsLink is a link with a note, as the format's sections are lists of.
type LLMsLink struct {
	Name, URL, Note string
}

// llmsSummaryMax keeps each line to roughly one sentence. The format's value is
// that the whole file fits comfortably in a context window beside the question
// being asked; pasting full posts in defeats it, and /llms-full.txt is the
// convention's answer for that, not this file.
const llmsSummaryMax = 200

// Render returns the body of /llms.txt, or "" when there is nothing truthful to
// say (no origin).
//
// The shape is the convention's (llmstxt.org): an H1 naming the site, a
// blockquote saying what it is, a paragraph of detail, then H2 sections that
// are lists of [name](url): notes, the last one "Optional", which a reader
// short of room may skip. Every reference is a link a crawler can follow; a
// bare "Feed: https://…" line is text to it, not a link.
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

	summary := oneLine(firstNonEmpty(d.Description, d.Tagline))
	if summary != "" {
		b.WriteString("> " + summary + "\n\n")
	}
	var detail []string
	if t := oneLine(d.Tagline); t != "" && t != summary {
		detail = append(detail, strings.TrimRight(t, ".")+".")
	}
	if a := oneLine(d.Author); a != "" {
		detail = append(detail, "Written by "+a+".")
	}
	detail = append(detail, "Posts are listed newest first, each with its date.")
	if !d.Generated.IsZero() {
		detail = append(detail, "This list was made "+d.Generated.UTC().Format("2006-01-02")+".")
	}
	b.WriteString(strings.Join(detail, " ") + "\n\n")

	writePosts := func(posts []LLMsPost, dated bool) {
		for _, p := range posts {
			if p.URL == "" || p.Title == "" {
				continue
			}
			b.WriteString("- [" + linkText(p.Title) + "](" + p.URL + ")")
			if s := clip(oneLine(p.Summary), llmsSummaryMax); s != "" {
				b.WriteString(": " + s)
			}
			if dated && !p.Published.IsZero() {
				b.WriteString(" (" + p.Published.UTC().Format("2006-01-02") + ")")
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if len(d.Pinned) > 0 {
		b.WriteString("## Start here\n\n")
		writePosts(d.Pinned, false)
	}
	if len(d.Topics) > 0 {
		b.WriteString("## Topics\n\n")
		for _, t := range d.Topics {
			b.WriteString("- [" + linkText(t.Name) + "](" + t.URL + ")")
			if t.Note != "" {
				b.WriteString(": " + oneLine(t.Note))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if len(d.Posts) == 0 {
		// Saying so beats an empty heading that reads like a truncated file.
		b.WriteString("No posts have been published yet.\n\n")
	} else {
		b.WriteString("## Posts\n\n")
		writePosts(d.Posts, true)
	}
	b.WriteString("## Optional\n\n")
	b.WriteString("- [Home](" + origin + "/): the newest writing\n")
	b.WriteString("- [Feed](" + origin + "/feed.xml): the newest posts, as RSS\n")
	b.WriteString("- [Sitemap](" + origin + "/sitemap.xml): every page on the site\n")
	return b.String()
}

// linkText is a title fit for a Markdown link's text: one line, and its
// square brackets escaped, since "[Go] Generics" would otherwise end the link
// text at its first "]".
func linkText(s string) string {
	return strings.NewReplacer(`[`, `\[`, `]`, `\]`).Replace(oneLine(s))
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
