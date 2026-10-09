// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"strings"
	"time"
)

// SearchQuery is what a mail search looks for: words, and the terms a person
// types beside them as other mail services read them. Every part given must
// match.
type SearchQuery struct {
	// Words are each looked for in the From, To and Subject, then the whole
	// message; a quoted phrase is one word.
	Words []string
	// From, To and Subject are each contained in that header.
	From, To, Subject string
	// Folder is the one folder to search; empty for all of them.
	Folder string
	// Label is a label the message carries (labels.go). The engine reads
	// which messages carry it into labelled before the scan; the folders know
	// nothing of labels.
	Label    string
	labelled map[string]bool
	// After is the first instant included and Before the first excluded;
	// zero is no bound.
	After, Before time.Time
	Attachment    bool
	Unread        bool
}

// Empty reports whether the query asks for nothing, which matches nothing
// rather than everything.
func (q SearchQuery) Empty() bool {
	return len(q.Words) == 0 && q.From == "" && q.To == "" && q.Subject == "" && q.Folder == "" && q.Label == "" &&
		q.After.IsZero() && q.Before.IsZero() && !q.Attachment && !q.Unread
}

// ParseSearchQuery reads what was typed into the search field: from:, to:,
// subject:, in: (a folder), label:, after: and before: (a day, YYYY-MM-DD or
// YYYY/MM/DD; before: excludes the day, as Gmail reads it), has:attachment
// and is:unread, each value one word or "quoted words". Anything else is a
// word. A term that cannot be read (a date that is not one, has: something
// else) stays a word, so it is looked for as typed rather than dropped
// unseen; a term with no value yet ("from:" while typing) asks for nothing,
// since as a word it would match every message's own header.
func ParseSearchQuery(s string) SearchQuery {
	var q SearchQuery
	word := func(tok string) {
		if w := unquote(tok); w != "" {
			q.Words = append(q.Words, w)
		}
	}
	for _, tok := range searchTokens(s) {
		name, val, _ := strings.Cut(tok, ":")
		name, val = strings.ToLower(name), unquote(val)
		// A token opening with a quote names no term ("from:x" quoted is a
		// phrase): its name starts with the quote, which no term's does.
		if !searchTermNames[name] {
			word(tok)
			continue
		}
		if val == "" {
			continue
		}
		read := true
		switch name {
		case "from":
			q.From = val
		case "to":
			q.To = val
		case "subject":
			q.Subject = val
		case "in":
			q.Folder = val
		case "label":
			q.Label = val
		case "after", "before":
			day, err := parseSearchDay(val)
			switch {
			case err != nil:
				read = false
			case name == "after":
				q.After = day
			default:
				q.Before = day
			}
		case "has":
			read = strings.EqualFold(val, "attachment")
			q.Attachment = q.Attachment || read
		case "is":
			read = strings.EqualFold(val, "unread")
			q.Unread = q.Unread || read
		}
		if !read {
			word(tok)
		}
	}
	return q
}

// String writes q back as search terms, in a fixed order and quoted where a
// value needs it, so ParseSearchQuery(q.String()) is q. It is the one form a
// saved search is kept and compared in, however it was first typed.
func (q SearchQuery) String() string {
	var parts []string
	quote := func(v string) string {
		if strings.ContainsAny(v, " \t\n:") {
			return `"` + v + `"`
		}
		return v
	}
	for _, w := range q.Words {
		parts = append(parts, quote(w))
	}
	for _, t := range [][2]string{{"from", q.From}, {"to", q.To}, {"subject", q.Subject}, {"in", q.Folder}, {"label", q.Label}} {
		if t[1] != "" {
			parts = append(parts, t[0]+":"+quote(t[1]))
		}
	}
	if !q.After.IsZero() {
		parts = append(parts, "after:"+q.After.Format("2006-01-02"))
	}
	if !q.Before.IsZero() {
		parts = append(parts, "before:"+q.Before.Format("2006-01-02"))
	}
	if q.Attachment {
		parts = append(parts, "has:attachment")
	}
	if q.Unread {
		parts = append(parts, "is:unread")
	}
	return strings.Join(parts, " ")
}

var searchTermNames = map[string]bool{"from": true, "to": true, "subject": true, "in": true, "label": true, "after": true, "before": true, "has": true, "is": true}

// searchTokens splits s on spaces outside double quotes, so `subject:"two
// words"` and `"a phrase"` are one token each.
func searchTokens(s string) []string {
	var out []string
	var cur strings.Builder
	quoted := false
	for _, r := range s {
		switch {
		case r == '"':
			quoted = !quoted
			cur.WriteRune(r)
		case !quoted && (r == ' ' || r == '\t' || r == '\n'):
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if t := strings.TrimSpace(cur.String()); t != "" {
		out = append(out, t)
	}
	return out
}

func unquote(s string) string { return strings.TrimSpace(strings.ReplaceAll(s, `"`, "")) }

func parseSearchDay(s string) (time.Time, error) {
	return time.Parse("2006-01-02", strings.ReplaceAll(s, "/", "-"))
}

// headersMatch reports whether what the folder listing already holds
// satisfies every term but the words, so a message that fails one is never
// read from disk.
func (q SearchQuery) headersMatch(sm StoredMessage) bool {
	contains := func(field, want string) bool {
		return want == "" || strings.Contains(strings.ToLower(field), strings.ToLower(want))
	}
	return contains(sm.From, q.From) && contains(sm.To, q.To) && contains(sm.Subject, q.Subject) &&
		(q.After.IsZero() || !sm.Date.Before(q.After)) &&
		(q.Before.IsZero() || sm.Date.Before(q.Before)) &&
		(!q.Attachment || sm.Attachment) && (!q.Unread || !sm.Seen) &&
		(q.Label == "" || q.labelled[sm.MessageID])
}
