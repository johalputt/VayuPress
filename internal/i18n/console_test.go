// SPDX-License-Identifier: Apache-2.0

package i18n

import (
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/language"
)

// TestTFillsPlaceholdersAndPluralsByCLDR — each language's own plural
// forms: Russian's one, few and many, Japanese's one form for all, an
// exact =0, # as the number.
func TestTFillsPlaceholdersAndPluralsByCLDR(t *testing.T) {
	msg := "{n, plural, =0 {nothing} one {# file from {who}} few {# files few} many {# files many} other {# files from {who}}}"
	for _, c := range []struct {
		lang string
		n    int
		want string
	}{
		{"en", 0, "nothing"}, {"en", 1, "1 file from Dana"}, {"en", 2, "2 files from Dana"},
		{"ru", 1, "1 file from Dana"}, {"ru", 3, "3 files few"}, {"ru", 5, "5 files many"}, {"ru", 21, "21 file from Dana"},
		{"ja", 1, "1 files from Dana"},
	} {
		if got := render(msg, mustTag(c.lang), map[string]any{"n": c.n, "who": "Dana"}, ""); got != c.want {
			t.Errorf("%s %d: %q, not %q", c.lang, c.n, got, c.want)
		}
	}
	if got := T("es", "{size}, shared by {who}", "size", "2 KB", "who", "dana@example.com"); got != "2 KB, compartido por dana@example.com" {
		t.Errorf("Spanish: %q", got)
	}
	if got := T("es", "A sentence no file has"); got != "A sentence no file has" {
		t.Errorf("an untranslated sentence: %q", got)
	}
	if got := T("xx", "Download"); got != "Download" {
		t.Errorf("an unknown language: %q", got)
	}
}

// TestThePseudoLanguageIsLongerAndKeepsItsPlaceholders.
func TestThePseudoLanguageIsLongerAndKeepsItsPlaceholders(t *testing.T) {
	got := T(Pseudo, "Send files to {who}", "who", "dana")
	if !strings.Contains(got, "dana") || !strings.HasPrefix(got, "[") || len([]rune(got)) < len("Send files to dana")*13/10 || strings.Contains(got, "Send") {
		t.Errorf("pseudo: %q", got)
	}
	if got := T(Pseudo, "{n, plural, one {# file} other {# files}}", "n", 2); !strings.Contains(got, "2 fílés") {
		t.Errorf("pseudo plural: %q", got)
	}
}

// TestALanguageIsChosenByThePersonThenTheBrowser.
func TestALanguageIsChosenByThePersonThenTheBrowser(t *testing.T) {
	for _, c := range []struct{ chosen, accept, want string }{
		{"de", "fr", "de"},
		{"", "fr-CA,fr;q=0.9,en;q=0.5", "fr"},
		{"", "zh-CN,zh;q=0.9", "zh-Hans"},
		{"", "pt-BR", "pt-BR"},
		{"", "hi-IN", "hi"},
		{"", "ar,fa;q=0.8", "en"},
		{"nonsense", "ja", "ja"},
		{"", "", "en"},
		{Pseudo, "fr", Pseudo},
	} {
		if got := Choose(c.chosen, c.accept); got != c.want {
			t.Errorf("chosen %q, browser %q: %q, not %q", c.chosen, c.accept, got, c.want)
		}
	}
}

// TestEveryLanguageHasEverySentenceWithItsPlaceholders — a lost {who} or a
// dropped plural fails here, not on a visitor's screen.
func TestEveryLanguageHasEverySentenceWithItsPlaceholders(t *testing.T) {
	en := catalogues["en"]
	if len(en) == 0 {
		t.Fatal("en.json is empty")
	}
	for _, l := range Languages[1:] {
		cat := catalogues[l.Tag]
		for msg := range en {
			tr, ok := cat[msg]
			if !ok || strings.TrimSpace(tr) == "" {
				t.Errorf("%s has no translation of %q", l.Tag, msg)
				continue
			}
			want, got := placeholders(msg), placeholders(tr)
			slices.Sort(want)
			slices.Sort(got)
			if !slices.Equal(want, got) {
				t.Errorf("%s: %q keeps %v, not %v", l.Tag, tr, got, want)
			}
		}
		for msg := range cat {
			if _, ok := en[msg]; !ok {
				t.Errorf("%s translates %q, which en.json does not have", l.Tag, msg)
			}
		}
	}
}

func mustTag(s string) language.Tag { return language.MustParse(s) }

// TestDatesAreWrittenAsEachLanguageWritesThem — a month standing alone and
// in a date (Russian's two forms), the day before the month, the year
// first, and English as it was.
func TestDatesAreWrittenAsEachLanguageWritesThem(t *testing.T) {
	at := time.Date(2026, 11, 20, 15, 4, 0, 0, time.UTC)
	for _, c := range []struct {
		lang string
		form DateForm
		want string
	}{
		{"en", DayMonthYearAt, "Friday 20 November 2026, 15:04"},
		{"en", MonthYear, "November 2026"},
		{"ru", MonthYear, "ноябрь 2026"},
		{"ru", DayMonthAt, "пятница, 20 ноября, 15:04"},
		{"de", DayMonth, "Freitag, 20. November"},
		{"es", DayMonthYearAt, "viernes, 20 de noviembre de 2026, 15:04"},
		{"ja", DayMonthAt, "11月20日 金曜日 15:04"},
		{"ko", MonthYear, "2026년 11월"},
		{"tr", Day, "20 Cuma"},
		{"xx", Day, "Friday 20"},
	} {
		if got := Date(c.lang, at, c.form); got != c.want {
			t.Errorf("%s %d: %q, not %q", c.lang, c.form, got, c.want)
		}
	}
	if heads := WeekdayHeads("de"); heads[0] != "Mo" || heads[6] != "So" {
		t.Errorf("German heads: %v", heads)
	}
}

// placeholders is the argument names a message uses, plurals included, in
// a canonical form for comparing a translation with its English.
func placeholders(msg string) []string {
	var out []string
	var walk func(s string)
	walk = func(s string) {
		for i := 0; i < len(s); i++ {
			if s[i] != '{' {
				continue
			}
			end := closing(s, i)
			if end < 0 {
				return
			}
			inner := s[i+1 : end]
			name, rest, isChoice := strings.Cut(inner, ",")
			if isChoice {
				out = append(out, strings.TrimSpace(name)+":plural")
				_, branches, _ := strings.Cut(rest, ",")
				for _, t := range pluralBranches(branches) {
					walk(t)
				}
			} else {
				out = append(out, strings.TrimSpace(name))
			}
			i = end
		}
	}
	walk(msg)
	return out
}
