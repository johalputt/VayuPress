// SPDX-License-Identifier: Apache-2.0

package classifier

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// hostCases are referrer hosts and the category each belongs to, stated
// here rather than read from the tables.
var hostCases = map[string]Category{
	"":                  Direct,
	"   ":               Direct,
	"\t":                Direct,
	"google.com":        Organic,
	"www.google.co.uk":  Organic,
	"GOOGLE.COM":        Organic,
	"bing.com":          Organic,
	"duckduckgo.com":    Organic,
	"search.brave.com":  Organic,
	"seznam.cz":         Organic,
	"gemini.google.com": AIAssisted,
	"chatgpt.com":       AIAssisted,
	// Seen on a live install: NotebookLM and Yuanbao are assistants, not
	// Google search or a referral; the Android Google app and DuckDuckGo's
	// no-AI host are search.
	"notebook.google.com":                     AIAssisted,
	"yuanbao.tencent.com":                     AIAssisted,
	"chat.deepseek.com":                       AIAssisted,
	"com.google.android.googlequicksearchbox": Organic,
	"noai.duckduckgo.com":                     Organic,
	"mail.google.com":                         Email,
	"outlook.office.com":                      Email,
	"teams.microsoft.com":                     Corporate,
	"app.slack.com":                           Corporate,
	"t.co":                                    Social,
	"x.com":                                   Social,
	"www.x.com":                               Social,
	"m.facebook.com":                          Social,
	"l.facebook.com":                          Social,
	"old.reddit.com":                          Social,
	"news.ycombinator.com":                    Social,
	"bsky.app":                                Social,
	"mastodon.social":                         Social,
	"web.telegram.org":                        Social,
	"youtu.be":                                Social,
	"example.com":                             Referral,
	"some.blog.io":                            Referral,
	"cnet.com":                                Referral,
	"dropbox.com":                             Referral,
	"wix.com":                                 Referral,
	"mygoogle.example":                        Referral,
	"notmastodon.com":                         Referral,
	"brave.com":                               Referral,
	"task.com":                                Referral,
	"search.brave.com.evil":                   Referral,
}

func TestHostCategory(t *testing.T) {
	for host, want := range hostCases {
		if got := HostCategory(host); got != want {
			t.Errorf("HostCategory(%q) = %q, want %q", host, got, want)
		}
	}
}

// The SQL a report groups by puts every host where the stated cases do.
func TestHostCategorySQLIsTheSameRule(t *testing.T) {
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for host, want := range hostCases {
		var got string
		if err := db.QueryRow(`WITH t(ref) AS (SELECT ?) SELECT `+HostCategorySQL("ref")+` FROM t`, host).Scan(&got); err != nil {
			t.Fatalf("%q: %v", host, err)
		}
		if Category(got) != want {
			t.Errorf("HostCategorySQL(%q) = %q, want %q", host, got, want)
		}
	}
}
