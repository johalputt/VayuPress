// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"

	"github.com/johalputt/vayupress/internal/i18n"
	"github.com/johalputt/vayupress/internal/theme"
)

// TestEveryThemeOptionWordIsTranslated — the Theme Studio and the store draw
// their options' and themes' words from the theme package, in English,
// through themeWord. An option, a choice or a store theme added there
// without its case in themeWord would show in English in every language; the pseudo-language changes every word it translates,
// so a word it leaves as it was is one themeWord does not know.
func TestEveryThemeOptionWordIsTranslated(t *testing.T) {
	check := func(o theme.Option) {
		words := []string{o.Label, o.Help}
		for _, c := range o.Choices {
			words = append(words, c.Label)
		}
		for _, w := range words {
			if w != "" && themeWord(i18n.Pseudo, w) == w {
				t.Errorf("option %q: %q has no case in themeWord", o.Key, w)
			}
		}
	}
	for _, o := range theme.AllOptions() {
		check(o)
	}
	for _, to := range theme.PerThemeOptions() {
		check(to.Option)
	}
	for _, o := range theme.HalcyonOptions() {
		check(o)
	}
	for _, e := range theme.Store() {
		m := e.Meta
		for _, w := range append([]string{m.Tagline, m.Description, m.Category}, m.Tags...) {
			if w != "" && themeWord(i18n.Pseudo, w) == w {
				t.Errorf("store theme %q: %q has no case in themeWord", m.Name, w)
			}
		}
	}
	for _, c := range theme.AllCategories() {
		if themeWord(i18n.Pseudo, c) == c {
			t.Errorf("store category %q has no case in themeWord", c)
		}
	}
}
