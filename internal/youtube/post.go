// SPDX-License-Identifier: Apache-2.0

package youtube

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/johalputt/vayupress/internal/blockrender"
	"github.com/johalputt/vayupress/internal/embeds"
)

// A description is the channel's text, not markup. It becomes Markdown
// paragraphs for the editor's paragraph blocks only after every ASCII
// punctuation mark in it is escaped, so nothing it says is read as emphasis,
// a link or HTML; the links, timestamps and hashtags below are the only
// markup it gains, and each is built here from what was matched.
var (
	urlRe = regexp.MustCompile(`https?://[^\s<>"'\x60]+`)
	// A timestamp: 3:15, 03:15 or 1:02:03, standing on its own.
	timeRe = regexp.MustCompile(`(^|[\s(\[])((?:\d{1,2}:)?\d{1,2}:\d{2})\b`)
	tagRe  = regexp.MustCompile(`(?:^|\s)#([\p{L}\p{M}\p{N}_]{2,50})`) // \p{M}: the vowel signs of Indic scripts
)

// maxTags is how many of a description's hashtags become the post's tags.
const maxTags = 10

// Hashtags returns a description's hashtags, lower-cased, in order, without
// repeats, at most maxTags.
func Hashtags(desc string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range tagRe.FindAllStringSubmatch(desc, -1) {
		t := strings.ToLower(m[1])
		if seen[t] || isDigits(t) {
			continue
		}
		seen[t] = true
		out = append(out, t)
		if len(out) == maxTags {
			break
		}
	}
	return out
}

// Blocks is a video's post in the editor's own blocks: the player, the
// description's paragraphs, and a line back to the video. cover is the local
// media address of its cover, or "".
func Blocks(v Video, cover string) []blockrender.Block {
	link := WatchURL(v.ID)
	player := blockrender.Block{
		Type: "embed", Kind: "video", Provider: "YouTube",
		URL: link, Title: v.Title, ThumbURL: cover,
		EmbedSrc: embeds.EmbedSrc("youtube", v.ID),
	}
	if v.Kind == KindShort {
		link = ShortURL(v.ID)
		player.URL = link
		player.Style = "tall"
	}
	out := []blockrender.Block{player}
	switch {
	case v.Kind == KindUpcoming && !v.Start.IsZero():
		out = append(out, blockrender.Block{Type: "paragraph", Text: escapeMD("Starts " + v.Start.UTC().Format("Monday 2 January 2006 at 15:04") + " UTC.")})
	case v.Kind == KindLive:
		out = append(out, blockrender.Block{Type: "paragraph", Text: "Live now\\."})
	}
	for _, p := range Paragraphs(v.Description, v.ID) {
		out = append(out, blockrender.Block{Type: "paragraph", Text: p})
	}
	out = append(out, blockrender.Block{Type: "paragraph", Text: "[Watch on YouTube](<" + link + ">)"})
	return out
}

// Paragraphs turns a description into Markdown paragraphs: blank lines part
// paragraphs, a line break stays a line break, and only http and https addresses and
// timestamps become links.
func Paragraphs(desc, videoID string) []string {
	desc = strings.ReplaceAll(desc, "\r\n", "\n")
	var out []string
	for _, para := range strings.Split(desc, "\n\n") {
		var lines []string
		for _, ln := range strings.Split(para, "\n") {
			if ln = strings.TrimSpace(ln); ln != "" {
				lines = append(lines, inline(ln, videoID))
			}
		}
		if len(lines) > 0 {
			// A backslash at a line's end is Markdown's line break.
			out = append(out, strings.Join(lines, "\\\n"))
		}
	}
	return out
}

// inline is one line of a description as Markdown: its addresses and
// timestamps as links, everything else escaped.
func inline(line, videoID string) string {
	var b strings.Builder
	rest := line
	for rest != "" {
		loc := urlRe.FindStringIndex(rest)
		if loc == nil {
			b.WriteString(timestamps(rest, videoID))
			break
		}
		b.WriteString(timestamps(rest[:loc[0]], videoID))
		u := trimURL(rest[loc[0]:loc[1]])
		if ok := safeLink(u); ok {
			b.WriteString("[" + escapeMD(u) + "](<" + u + ">)")
		} else {
			b.WriteString(escapeMD(u))
		}
		rest = rest[loc[0]+len(u):]
	}
	return b.String()
}

// timestamps escapes s, making each timestamp a link that opens the video at
// that moment.
func timestamps(s, videoID string) string {
	var b strings.Builder
	last := 0
	for _, m := range timeRe.FindAllStringSubmatchIndex(s, -1) {
		secs, ok := seconds(s[m[4]:m[5]])
		if !ok {
			continue
		}
		b.WriteString(escapeMD(s[last:m[4]]))
		b.WriteString("[" + escapeMD(s[m[4]:m[5]]) + "](<" + WatchURL(videoID) + "&t=" + strconv.Itoa(secs) + "s>)")
		last = m[5]
	}
	b.WriteString(escapeMD(s[last:]))
	return b.String()
}

// seconds reads h:mm:ss or m:ss, refusing minutes or seconds of 60 or more.
func seconds(t string) (int, bool) {
	parts := strings.Split(t, ":")
	total := 0
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || (i > 0 && n >= 60) {
			return 0, false
		}
		total = total*60 + n
	}
	return total, true
}

// trimURL drops the punctuation a sentence puts after an address, and a
// closing bracket the address did not open.
func trimURL(u string) string {
	for u != "" {
		last := u[len(u)-1]
		switch {
		case strings.ContainsRune(".,;:!?'\"", rune(last)):
		case last == ')' && strings.Count(u, "(") < strings.Count(u, ")"):
		case last == ']' && strings.Count(u, "[") < strings.Count(u, "]"):
		default:
			return u
		}
		u = u[:len(u)-1]
	}
	return u
}

// safeLink reports whether u, which urlRe already holds to http or https with no
// space or angle bracket, has a host and can stand as a Markdown destination
// in angle brackets, where a backslash would escape the bracket that ends it.
func safeLink(u string) bool {
	p, err := url.Parse(u)
	return err == nil && p.Host != "" && !strings.Contains(u, "\\")
}

// escapeMD backslash-escapes every ASCII punctuation mark, which CommonMark
// reads as the mark itself.
func escapeMD(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 128 && strings.ContainsRune("!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~", r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}
