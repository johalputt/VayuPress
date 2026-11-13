// SPDX-License-Identifier: Apache-2.0

package i18n

// console.go — the console's and its public pages' sentences in the
// languages of the first wave, keyed by the English sentence itself.
//
// A sentence is written once, in English, through T:
//
//	i18n.T(lang, "Shared by {who}", "who", addr)
//	i18n.T(lang, "{n, plural, one {# file} other {# files}}", "n", 3)
//
// Placeholders are ICU's: {name} is an argument; {name, plural, ...} picks
// the form CLDR gives that number in that language (one, few, many, other,
// or =0 and the like), with # the number. Each language is one JSON file in
// locales/, English sentence to translation; locales/en.json is the list of
// every sentence. A sentence with no translation is shown in English, never
// empty. Tests hold the files to the code: every sentence T is given is in
// en.json, every language has every sentence, and every translation keeps
// the English's placeholders and plural arguments.
//
// Right-to-left languages are not in this wave: the layout stays as it is
// (languages/PLAN in the operator's notes), and an interface in Arabic on a
// left-to-right page reads as broken. Mail written in them is shown in its
// own direction regardless.

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/text/feature/plural"
	"golang.org/x/text/language"
)

//go:embed locales/*.json
var localeFiles embed.FS

// Language is one the console speaks: its tag and its own name for itself.
type Language struct {
	Tag  string
	Name string
}

// Languages is the first wave, in the order the language menu lists them.
var Languages = []Language{
	{"en", "English"}, {"es", "Español"}, {"pt-BR", "Português (Brasil)"}, {"fr", "Français"},
	{"de", "Deutsch"}, {"it", "Italiano"}, {"ru", "Русский"}, {"tr", "Türkçe"},
	{"zh-Hans", "中文（简体）"}, {"ja", "日本語"}, {"ko", "한국어"}, {"id", "Bahasa Indonesia"}, {"hi", "हिन्दी"},
}

// Pseudo is the pseudo-language: the English accented and 40 % longer,
// chosen only by name, to find a sentence left out of T or a label that
// does not fit, without reading every language.
const Pseudo = "en-XA"

var (
	catalogues = map[string]map[string]string{}
	tags       []language.Tag
	matcher    language.Matcher
)

func init() {
	for _, l := range Languages {
		tags = append(tags, language.MustParse(l.Tag))
		b, err := localeFiles.ReadFile("locales/" + l.Tag + ".json")
		if err != nil {
			panic("i18n: no locales/" + l.Tag + ".json")
		}
		m := map[string]string{}
		if err := json.Unmarshal(b, &m); err != nil {
			panic("i18n: locales/" + l.Tag + ".json: " + err.Error())
		}
		catalogues[l.Tag] = m
	}
	matcher = language.NewMatcher(tags)
}

// Choose is the language to speak: the person's own choice when it is one
// of ours (or the pseudo-language), else the best of what their browser
// asks for, else English.
func Choose(chosen, acceptLanguage string) string {
	if chosen == Pseudo {
		return Pseudo
	}
	if _, ok := catalogues[chosen]; ok {
		return chosen
	}
	if acceptLanguage == "" {
		return "en"
	}
	prefs, _, err := language.ParseAcceptLanguage(acceptLanguage)
	if err != nil || len(prefs) == 0 {
		return "en"
	}
	// With nothing near, the matcher answers its first tag, English.
	_, i, _ := matcher.Match(prefs...)
	return Languages[i].Tag
}

type langKey struct{}

// WithLang is ctx carrying lang, the language the page it serves is written
// in.
func WithLang(ctx context.Context, lang string) context.Context {
	return context.WithValue(ctx, langKey{}, lang)
}

// Lang is the language ctx's page is written in: English when none was set.
func Lang(ctx context.Context) string {
	if l, ok := ctx.Value(langKey{}).(string); ok && l != "" {
		return l
	}
	return "en"
}

// T is msg in lang, with its placeholders filled from args (name, value
// pairs). An unknown language, or a sentence it has no translation for, is
// English.
func T(lang, msg string, args ...any) string {
	vals := map[string]any{}
	for i := 0; i+1 < len(args); i += 2 {
		if k, ok := args[i].(string); ok {
			vals[k] = args[i+1]
		}
	}
	text := msg
	if lang == Pseudo {
		text = pseudo(msg)
	} else if tr := catalogues[lang][msg]; tr != "" {
		text = tr
	}
	tag := language.English
	if lang != Pseudo {
		if t, err := language.Parse(lang); err == nil {
			tag = t
		}
	}
	return render(text, tag, vals, "")
}

// render fills text's placeholders; hash is what # stands for inside a
// plural branch.
func render(text string, tag language.Tag, vals map[string]any, hash string) string {
	var b strings.Builder
	for i := 0; i < len(text); {
		c := text[i]
		if c == '#' && hash != "" {
			b.WriteString(hash)
			i++
			continue
		}
		if c != '{' {
			b.WriteByte(c)
			i++
			continue
		}
		end := closing(text, i)
		if end < 0 {
			b.WriteString(text[i:])
			break
		}
		b.WriteString(placeholder(text[i+1:end], tag, vals))
		i = end + 1
	}
	return b.String()
}

// closing is the index of the brace that closes the one at open, or -1.
func closing(text string, open int) int {
	depth := 0
	for j := open; j < len(text); j++ {
		switch text[j] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return j
			}
		}
	}
	return -1
}

// placeholder is one {…}: an argument, or a plural choice.
func placeholder(inner string, tag language.Tag, vals map[string]any) string {
	name, rest, isChoice := strings.Cut(inner, ",")
	name = strings.TrimSpace(name)
	if !isChoice {
		return fmt.Sprint(vals[name])
	}
	kind, branches, _ := strings.Cut(rest, ",")
	if strings.TrimSpace(kind) != "plural" {
		return "{" + inner + "}"
	}
	n := toInt(vals[name])
	forms := pluralBranches(branches)
	if t, ok := forms["="+strconv.Itoa(n)]; ok {
		return render(t, tag, vals, strconv.Itoa(n))
	}
	cat := map[plural.Form]string{plural.Zero: "zero", plural.One: "one", plural.Two: "two", plural.Few: "few", plural.Many: "many", plural.Other: "other"}[plural.Cardinal.MatchPlural(tag, abs(n), 0, 0, 0, 0)]
	t, ok := forms[cat]
	if !ok {
		t = forms["other"]
	}
	return render(t, tag, vals, strconv.Itoa(n))
}

// pluralBranches reads "one {…} other {…}" into its forms.
func pluralBranches(s string) map[string]string {
	out := map[string]string{}
	for i := 0; i < len(s); {
		open := strings.IndexByte(s[i:], '{')
		if open < 0 {
			break
		}
		key := strings.TrimSpace(s[i : i+open])
		end := closing(s, i+open)
		if end < 0 {
			break
		}
		out[key] = s[i+open+1 : end]
		i = end + 1
	}
	return out
}

func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// pseudo is msg accented and lengthened, its placeholders kept as written.
func pseudo(msg string) string {
	accent := strings.NewReplacer("a", "á", "e", "é", "i", "í", "o", "ö", "u", "ü", "A", "Å", "E", "É", "O", "Ö", "c", "ç", "n", "ñ")
	var b strings.Builder
	for i := 0; i < len(msg); {
		if msg[i] == '{' {
			end := closing(msg, i)
			if end > 0 {
				inner := msg[i+1 : end]
				if name, rest, isChoice := strings.Cut(inner, ","); isChoice {
					kind, branches, _ := strings.Cut(rest, ",")
					b.WriteString("{" + name + "," + kind + ",")
					for k, t := range pluralBranches(branches) {
						b.WriteString(" " + k + " {" + pseudo(t) + "}")
					}
					b.WriteString("}")
				} else {
					b.WriteString(msg[i : end+1])
				}
				i = end + 1
				continue
			}
		}
		j := i
		for j < len(msg) && msg[j] != '{' {
			j++
		}
		b.WriteString(accent.Replace(msg[i:j]))
		i = j
	}
	out := b.String()
	return "[" + out + strings.Repeat("~", len(msg)*2/5) + "]"
}
