// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"strings"
	"time"
)

// ReadSearchSentence reads a search typed as a sentence ("pdf from priya
// last month") into the terms ParseSearchQuery reads, so the person sees
// them as chips and can change them before searching. Only the shapes below
// are read; every other word stays a word, so a sentence it does not
// understand searches as typed. Terms already typed are kept as they are.
//
//   - "from X", "by X": From, when no from: was typed; "to X": To.
//   - "unread": Unread. "attachment(s)", "attached", "file(s)", with an
//     optional "with" before: Attachment.
//   - a file kind (pdf, doc, docx, xls, xlsx, ppt, pptx, zip, jpg, jpeg,
//     png): Attachment, and the word stays, since the file's name is in the
//     message the scan reads.
//   - "in" and a standard folder: Folder.
//   - "today", "yesterday", "this|last week|month|year", a month's name
//     (the latest one not in the future), optionally "in" before it and a
//     year after it: After and Before, in UTC days as the search reads them.
//     "may" is read as the month only after "in".
//
// now is the reader's clock; tests give a fixed one.
func ReadSearchSentence(s string, now time.Time) SearchQuery {
	q := ParseSearchQuery(s)
	words := q.Words
	q.Words = nil
	day := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC)
	dated := !q.After.IsZero() || !q.Before.IsZero()
	setDays := func(after, before time.Time) {
		if dated {
			return
		}
		q.After, q.Before, dated = after, before, true
	}
	lower := func(i int) string {
		if i < 0 || i >= len(words) {
			return ""
		}
		return strings.ToLower(words[i])
	}
	for i := 0; i < len(words); i++ {
		w := lower(i)
		next := lower(i + 1)
		switch {
		case (w == "from" || w == "by") && next != "" && q.From == "":
			q.From = words[i+1]
			i++
		case w == "to" && next != "" && q.To == "":
			q.To = words[i+1]
			i++
		case w == "unread":
			q.Unread = true
		case w == "with" && attachmentWords[next]:
			q.Attachment = true
			i++
		case attachmentWords[w]:
			q.Attachment = true
		case fileKinds[w]:
			q.Attachment = true
			q.Words = append(q.Words, words[i])
		case w == "in" && standardFolder(next) != "" && q.Folder == "":
			q.Folder = standardFolder(next)
			i++
		case w == "today" && !dated:
			setDays(day, time.Time{})
		case w == "yesterday" && !dated:
			setDays(day.AddDate(0, 0, -1), day)
		case (w == "this" || w == "last") && periodStart(next, day) != nil && !dated:
			start := *periodStart(next, day)
			if w == "this" {
				setDays(start, time.Time{})
			} else {
				prev := periodBack(next, start)
				setDays(prev, start)
			}
			i++
		case w == "in" && monthNames[next] != 0 && !dated:
			i++
			i += readMonth(words, i, day, setDays)
		case monthNames[w] != 0 && w != "may" && !dated: // "may" alone is the verb; "in may" is the month
			i += readMonth(words, i, day, setDays)
		default:
			q.Words = append(q.Words, words[i])
		}
	}
	return q
}

// readMonth reads the month named at words[i], and a year after it if one
// follows, setting the days through set; it returns how many words past i
// it used.
func readMonth(words []string, i int, day time.Time, set func(after, before time.Time)) int {
	m := monthNames[strings.ToLower(words[i])]
	year, used := day.Year(), 0
	if i+1 < len(words) {
		if y, ok := searchYear(words[i+1]); ok {
			year, used = y, 1
		}
	}
	if used == 0 && m > day.Month() {
		year--
	}
	start := time.Date(year, m, 1, 0, 0, 0, 0, time.UTC)
	set(start, start.AddDate(0, 1, 0))
	return used
}

// searchYear reads a four-digit year from 1990 to 2100.
func searchYear(s string) (int, bool) {
	if len(s) != 4 {
		return 0, false
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, n >= 1990 && n <= 2100
}

// periodStart is the first day of the week (from Monday), month or year
// that day is in, or nil for any other word.
func periodStart(unit string, day time.Time) *time.Time {
	var t time.Time
	switch unit {
	case "week":
		back := (int(day.Weekday()) + 6) % 7
		t = day.AddDate(0, 0, -back)
	case "month":
		t = time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, time.UTC)
	case "year":
		t = time.Date(day.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
	default:
		return nil
	}
	return &t
}

// periodBack is the start of the period before the one starting at start.
func periodBack(unit string, start time.Time) time.Time {
	switch unit {
	case "week":
		return start.AddDate(0, 0, -7)
	case "month":
		return start.AddDate(0, -1, 0)
	}
	return start.AddDate(-1, 0, 0)
}

// standardFolder is the standard folder a word names, as the folders are
// spelt, or "".
func standardFolder(w string) string {
	for _, f := range StandardFolders {
		if strings.EqualFold(f, w) {
			return f
		}
	}
	return ""
}

var attachmentWords = map[string]bool{"attachment": true, "attachments": true, "attached": true, "file": true, "files": true}

var fileKinds = map[string]bool{"pdf": true, "doc": true, "docx": true, "xls": true, "xlsx": true, "ppt": true, "pptx": true, "zip": true, "jpg": true, "jpeg": true, "png": true}

var monthNames = map[string]time.Month{
	"january": 1, "february": 2, "march": 3, "april": 4, "may": 5, "june": 6, "july": 7, "august": 8,
	"september": 9, "october": 10, "november": 11, "december": 12,
	"jan": 1, "feb": 2, "mar": 3, "apr": 4, "jun": 6, "jul": 7, "aug": 8, "sep": 9, "sept": 9, "oct": 10, "nov": 11, "dec": 12,
}
