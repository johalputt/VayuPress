// SPDX-License-Identifier: Apache-2.0

package render

import (
	"html/template"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// halcyon_content.go — the presentation Halcyon gives a post's body when the
// page is rendered: the opening quotation as a standfirst, table cells
// labelled with their column so a phone can stack a row, an anchor on every
// section heading for the outline, and runs of capitals set in small caps.
//
// None of it touches the stored post. It runs on the sanitised HTML the
// renderer already produced, as a single token pass, and its output exists
// only in the page. Content repairs are a separate, tested change.

// OutlineEntry is one section of a post, for the outline beside it.
type OutlineEntry struct {
	ID   string
	Text string
}

// halcyonBody is a post's body as Halcyon presents it.
type halcyonBody struct {
	Standfirst template.HTML // the opening quotation, or ""
	Body       template.HTML
	Outline    []OutlineEntry
}

// capsRe matches a run of two or more capitals standing as a word: HTML, SQL,
// VISIBLE. The ASCII range is deliberate: a broken character such as the
// mojibake in 3au is not a capital and must not be dressed as one.
var capsRe = regexp.MustCompile(`\b[A-Z][A-Z0-9]*[A-Z]\b`)

// capsSkip are the elements whose text keeps its capitals as written: code and
// its kin, where case is meaning; links and headings, which have their own
// voice; tables, where capitals are usually labels; and abbr, which already
// says what the capitals are.
var capsSkip = map[atom.Atom]bool{
	atom.Pre: true, atom.Code: true, atom.Kbd: true, atom.Samp: true, atom.Var: true,
	atom.A: true, atom.Abbr: true, atom.Table: true,
	atom.H1: true, atom.H2: true, atom.H3: true, atom.H4: true, atom.H5: true, atom.H6: true,
}

// halcyonTreat presents a sanitised post body. smallCaps is the operator's
// Running text option.
func halcyonTreat(sanitised string, smallCaps bool) halcyonBody {
	body, standfirst := splitStandfirst(sanitised)
	var outline []OutlineEntry
	usedIDs := map[string]bool{}
	// Anchors the post already carries are taken first, so one made for a
	// heading without an anchor can never collide with one written later.
	for _, id := range existingIDs(body) {
		usedIDs[id] = true
	}

	var out strings.Builder
	// A table and a section heading are each written out once complete: the
	// table's wrapper depends on whether its columns are named, and a heading's
	// anchor on its text.
	var tbl, h2 strings.Builder
	var (
		inTable, inThead, inH2, inTH bool
		tableDepth, row, col         int
		heads                        []string
		thText, h2Text               strings.Builder
		h2Open                       html.Token
		skip                         int // depth inside capsSkip elements
	)
	w := func(s string) {
		switch {
		case inH2:
			h2.WriteString(s)
		case inTable:
			tbl.WriteString(s)
		default:
			out.WriteString(s)
		}
	}
	z := html.NewTokenizer(strings.NewReader(body))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		tok := z.Token()
		switch tt {
		case html.StartTagToken, html.SelfClosingTagToken:
			if tt == html.StartTagToken && capsSkip[tok.DataAtom] {
				skip++
			}
			switch tok.DataAtom {
			case atom.Table:
				// A table inside a table belongs to its cell; only the
				// outermost is wrapped and labelled.
				tableDepth++
				if tableDepth == 1 {
					inTable, row, heads = true, -1, nil
					tbl.Reset()
				}
			case atom.Thead:
				inThead = true
			case atom.Tr:
				if tableDepth == 1 {
					row++
					col = 0
				}
			case atom.Th:
				if tableDepth != 1 {
					break
				}
				// The column names are the head row's cells: the thead's, or
				// the first row's when a table has no thead.
				if inThead || (row == 0 && len(heads) == col) {
					inTH = true
					thText.Reset()
				}
				col++
			case atom.Td:
				if tableDepth != 1 {
					break
				}
				if col < len(heads) && heads[col] != "" && attr(tok, "data-label") == "" {
					tok.Attr = append(tok.Attr, html.Attribute{Key: "data-label", Val: heads[col]})
				}
				col++
			case atom.H2:
				if !inTable && !inH2 {
					inH2, h2Open = true, tok
					h2.Reset()
					h2Text.Reset()
					continue
				}
			}
			w(tok.String())
		case html.EndTagToken:
			if capsSkip[tok.DataAtom] && skip > 0 {
				skip--
			}
			switch tok.DataAtom {
			case atom.Thead:
				inThead = false
			case atom.Th:
				if inTH {
					inTH = false
					heads = append(heads, strings.Join(strings.Fields(thText.String()), " "))
				}
			case atom.Table:
				if tableDepth > 0 {
					tableDepth--
				}
				if inTable && tableDepth == 0 {
					tbl.WriteString(tok.String())
					inTable = false
					// A table whose columns are named can be set as labelled
					// blocks on a phone; one without stays a table and wraps
					// inside its cells.
					stack := ""
					if len(heads) > 0 {
						stack = " data-stack"
					}
					out.WriteString(`<div class="tbl"` + stack + `>` + tbl.String() + `</div>`)
					continue
				}
			case atom.H2:
				if inH2 {
					inH2 = false
					text := strings.Join(strings.Fields(h2Text.String()), " ")
					id := attr(h2Open, "id")
					if id == "" && text != "" {
						id = uniqueID(slugify(text), usedIDs)
						h2Open.Attr = append(h2Open.Attr, html.Attribute{Key: "id", Val: id})
					}
					if text != "" {
						outline = append(outline, OutlineEntry{ID: id, Text: text})
					}
					w(h2Open.String() + h2.String() + tok.String())
					continue
				}
			}
			w(tok.String())
		case html.TextToken:
			if inTH {
				thText.WriteString(tok.Data)
			}
			if inH2 {
				h2Text.WriteString(tok.Data)
			}
			if smallCaps && skip == 0 {
				w(capsHTML(tok.Data))
			} else {
				w(tok.String())
			}
		default:
			w(tok.String())
		}
	}
	// A post cut off mid-table or mid-heading still shows everything it has.
	if inH2 {
		out.WriteString(h2Open.String() + h2.String())
	}
	if inTable {
		out.WriteString(tbl.String())
	}
	sf := template.HTML("")
	if standfirst != "" {
		if smallCaps {
			standfirst = string(halcyonTreat(standfirst, true).Body)
		}
		sf = template.HTML(standfirst) //nolint:gosec // G203: sanitised by renderContentHTML before it reached here
	}
	return halcyonBody{Standfirst: sf, Body: template.HTML(out.String()), Outline: outline} //nolint:gosec // G203: as above
}

// existingIDs lists the id attributes already in a body.
func existingIDs(body string) []string {
	var ids []string
	z := html.NewTokenizer(strings.NewReader(body))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return ids
		case html.StartTagToken, html.SelfClosingTagToken:
			if id := attr(z.Token(), "id"); id != "" {
				ids = append(ids, id)
			}
		}
	}
}

// splitStandfirst takes a post's opening quotation as its standfirst. Only a
// blockquote that is the very first thing in the post qualifies: one met later
// is a quotation in the argument, and lifting it out would change its meaning.
func splitStandfirst(body string) (rest, standfirst string) {
	trimmed := strings.TrimLeft(body, " \t\r\n")
	if !strings.HasPrefix(trimmed, "<blockquote>") {
		return body, ""
	}
	end := strings.Index(trimmed, "</blockquote>")
	if end < 0 || strings.Contains(trimmed[len("<blockquote>"):end], "<blockquote") {
		return body, ""
	}
	inner := strings.TrimSpace(trimmed[len("<blockquote>"):end])
	// A bare quotation of one paragraph reads as the standfirst; unwrap it so
	// the standfirst is one block of text, not a paragraph inside a div.
	if strings.HasPrefix(inner, "<p>") && strings.HasSuffix(inner, "</p>") && strings.Count(inner, "<p>") == 1 {
		inner = inner[3 : len(inner)-4]
	}
	return trimmed[end+len("</blockquote>"):], inner
}

// capsHTML escapes text and sets each run of capitals in small caps. The
// letters are unchanged; only their presentation is.
func capsHTML(text string) string {
	esc := html.EscapeString(text)
	return capsRe.ReplaceAllString(esc, `<span class="sc">$0</span>`)
}

// capsText is capsHTML for a plain-text excerpt in a template.
func capsText(on bool) func(string) template.HTML {
	return func(s string) template.HTML {
		if !on {
			return template.HTML(html.EscapeString(s))
		}
		return template.HTML(capsHTML(s)) //nolint:gosec // G203: capsHTML escapes s before wrapping
	}
}

func attr(t html.Token, key string) string {
	for _, a := range t.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

var slugDropRe = regexp.MustCompile(`[^a-z0-9]+`)

// slugify makes a heading's anchor: lower case, words joined by hyphens.
func slugify(s string) string {
	s = strings.Trim(slugDropRe.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if s == "" {
		s = "section"
	}
	if len(s) > 64 {
		s = strings.TrimRight(s[:64], "-")
	}
	return s
}

func uniqueID(base string, used map[string]bool) string {
	id := base
	for n := 2; used[id]; n++ {
		id = base + "-" + strconv.Itoa(n)
	}
	used[id] = true
	return id
}
