// SPDX-License-Identifier: Apache-2.0

package tagrules

import (
	"reflect"
	"strings"
	"testing"
)

// One key per tag, whatever the case and separators it was written with; the
// symbols that make C++, C# and .NET what they are survive.
func TestAKeyIsOneTagHoweverWritten(t *testing.T) {
	for in, want := range map[string]string{
		"Machine Learning":     "machine-learning",
		"machine_learning":     "machine-learning",
		"  machine--learning ": "machine-learning",
		"C++":                  "c++",
		"C#":                   "c#",
		".NET":                 ".net",
		"Node.js":              "node.js",
		"Zürich":               "zürich",
		"東京":                   "東京",
		// Stop-words that are also topics on a technical site.
		"IT":  "it",
		"CAN": "can",
		"D":   "d",
		"#1":  "#1",
	} {
		if got := Key(in); got != want {
			t.Errorf("Key(%q) = %q, want %q", in, got, want)
		}
	}
}

// What is no tag: empty, a stop-word, longer than a topic.
func TestWhatIsNoTag(t *testing.T) {
	for _, in := range []string{"", " - ", "with", "Which", "about", strings.Repeat("a", MaxLen+1), "...", "#", "+", "..", ". #"} {
		if got := Key(in); got != "" {
			t.Errorf("Key(%q) = %q, want no tag", in, got)
		}
	}
	if Key(strings.Repeat("a", MaxLen)) == "" {
		t.Error("a tag of exactly MaxLen characters was refused")
	}
}

// A post keeps its tags in the order sent, each once, shown as written.
func TestCleanKeepsOrderAndDisplay(t *testing.T) {
	got := Clean([]string{"Machine  Learning", "with", "machine-learning", "Go"}, nil)
	if !reflect.DeepEqual(got, []string{"Machine Learning", "Go"}) {
		t.Errorf("got %q", got)
	}
	if Clean([]string{"with", "the"}, nil) != nil {
		t.Error("a post with only stop-words keeps tags")
	}
}

// Past MaxPerPost the rarest are kept, in the order sent; without
// frequencies, the first.
func TestCleanKeepsTheRarest(t *testing.T) {
	var tags []string
	freq := map[string]int{}
	for i := 0; i < MaxPerPost+3; i++ {
		k := "t" + string(rune('a'+i))
		tags = append(tags, k)
		freq[k] = 1000 - i // later tags rarer
	}
	got := Clean(tags, func(k string) int { return freq[k] })
	if len(got) != MaxPerPost || got[0] != tags[3] || got[MaxPerPost-1] != tags[len(tags)-1] {
		t.Errorf("rarest kept: %q", got)
	}
	if got := Clean(tags, nil); len(got) != MaxPerPost || got[0] != tags[0] {
		t.Errorf("first kept: %q", got)
	}
}

// A tag stored as a JSON array by the importers before the rules shows as the
// tag it was, not with the array's brackets and quotes around it.
func TestDisplayTrimsWhatSurroundsATag(t *testing.T) {
	got := Clean([]string{`["news"`, ` "Go Lang"]`, ".NET", "C#"}, nil)
	if !reflect.DeepEqual(got, []string{"news", "Go Lang", ".NET", "C#"}) {
		t.Errorf("got %q", got)
	}
}
