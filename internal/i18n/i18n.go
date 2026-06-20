// Package i18n provides lightweight internationalisation for the public site:
// a message catalog keyed by BCP-47 language tag, with HTTP Accept-Language
// negotiation. It has no external dependencies and no runtime network calls.
//
// The catalog ships with built-in English strings; operators may override or
// add languages at runtime (e.g. from site settings). Lookups always fall back
// to the default language, so a missing translation degrades gracefully to
// English rather than showing an empty string.
package i18n

import (
	"sort"
	"strconv"
	"strings"
	"sync"
)

// DefaultLang is the fallback language tag.
const DefaultLang = "en"

// builtins are the default English UI strings. Keys are stable identifiers.
var builtins = map[string]string{
	"nav.home":           "Home",
	"nav.feed":           "Feed",
	"nav.console":        "Console",
	"article.readtime":   "min read",
	"article.related":    "Related articles",
	"article.published":  "Published",
	"paywall.members":    "This post is for members.",
	"paywall.paid":       "This post is for paid subscribers.",
	"paywall.signin":     "Sign in",
	"paywall.subscribe":  "Subscribe",
	"comment.submit":     "Post comment",
	"comment.pending":    "Your comment is awaiting moderation.",
	"footer.poweredby":   "Powered by",
	"search.placeholder": "Search…",
	"home.tagline":       "Writing, governed at runtime.",
}

// Catalog is a thread-safe, multi-language message store.
type Catalog struct {
	mu      sync.RWMutex
	langs   map[string]map[string]string // lang -> key -> value
	defLang string
}

// New returns a catalog seeded with the built-in English strings.
func New() *Catalog {
	c := &Catalog{
		langs:   map[string]map[string]string{DefaultLang: cloneMap(builtins)},
		defLang: DefaultLang,
	}
	return c
}

func cloneMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// SetLanguage installs (or replaces) a language's message map. Keys not present
// fall back to the default language at lookup time.
func (c *Catalog) SetLanguage(lang string, messages map[string]string) {
	lang = normalizeLang(lang)
	if lang == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.langs[lang] = cloneMap(messages)
}

// Languages returns the sorted set of available language tags.
func (c *Catalog) Languages() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, 0, len(c.langs))
	for l := range c.langs {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

// Has reports whether a language is available.
func (c *Catalog) Has(lang string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.langs[normalizeLang(lang)]
	return ok
}

// T translates key into lang, falling back to the default language and finally
// to the key itself when no translation exists.
func (c *Catalog) T(lang, key string) string {
	lang = normalizeLang(lang)
	c.mu.RLock()
	defer c.mu.RUnlock()
	if m, ok := c.langs[lang]; ok {
		if v, ok := m[key]; ok {
			return v
		}
	}
	if m, ok := c.langs[c.defLang]; ok {
		if v, ok := m[key]; ok {
			return v
		}
	}
	return key
}

// Messages returns a merged map for lang (default language overlaid by lang),
// suitable for handing to a template as a translation table.
func (c *Catalog) Messages(lang string) map[string]string {
	lang = normalizeLang(lang)
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := map[string]string{}
	if m, ok := c.langs[c.defLang]; ok {
		for k, v := range m {
			out[k] = v
		}
	}
	if lang != c.defLang {
		if m, ok := c.langs[lang]; ok {
			for k, v := range m {
				out[k] = v
			}
		}
	}
	return out
}

// Negotiate picks the best available language from an Accept-Language header.
// It honours quality values and matches on the primary subtag (e.g. "fr-CA"
// matches an available "fr"). Returns the default language when nothing matches.
func (c *Catalog) Negotiate(acceptLanguage string) string {
	if acceptLanguage == "" {
		return c.defLang
	}
	type pref struct {
		tag string
		q   float64
	}
	var prefs []pref
	for _, part := range strings.Split(acceptLanguage, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		tag := part
		q := 1.0
		if i := strings.Index(part, ";"); i >= 0 {
			tag = strings.TrimSpace(part[:i])
			qs := part[i+1:]
			if j := strings.Index(qs, "q="); j >= 0 {
				if f, err := strconv.ParseFloat(strings.TrimSpace(qs[j+2:]), 64); err == nil {
					q = f
				}
			}
		}
		prefs = append(prefs, pref{normalizeLang(tag), q})
	}
	sort.SliceStable(prefs, func(i, j int) bool { return prefs[i].q > prefs[j].q })

	c.mu.RLock()
	defer c.mu.RUnlock()
	// First pass: exact tag match.
	for _, p := range prefs {
		if p.tag == "*" {
			continue
		}
		if _, ok := c.langs[p.tag]; ok {
			return p.tag
		}
	}
	// Second pass: primary-subtag match (fr-CA -> fr).
	for _, p := range prefs {
		primary := primarySubtag(p.tag)
		if _, ok := c.langs[primary]; ok {
			return primary
		}
	}
	return c.defLang
}

func normalizeLang(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	return s
}

func primarySubtag(s string) string {
	if i := strings.Index(s, "-"); i >= 0 {
		return s[:i]
	}
	return s
}
