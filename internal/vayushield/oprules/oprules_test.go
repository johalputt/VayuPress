// SPDX-License-Identifier: Apache-2.0

package oprules

import (
	"strings"
	"testing"
	"time"
)

func facts(path string) Facts {
	return Facts{Path: path, Method: "GET", Country: "IN", Client: "human", Score: 0.1,
		Header: func(n string) string {
			if n == "X-Probe" {
				return "ok"
			}
			return ""
		}}
}

// Each condition, one rule each, so a rule that matched for the wrong reason
// cannot pass for the right one.
func TestEachConditionMatchesWhatItSays(t *testing.T) {
	for _, tc := range []struct {
		rule  string
		f     Facts
		match bool
	}{
		{"block path=/wp-login.php", facts("/wp-login.php"), true},
		{"block path=/wp-login.php", facts("/wp-login.php.bak"), false},
		{"block path^=/wp-", facts("/wp-admin/x"), true},
		{"block path^=/wp-", facts("/blog/wp-"), false},
		{"block path*=.env", facts("/x/.env.local"), true},
		{"block method=POST,PUT", facts("/"), false},
		{"block country=cn,in", facts("/"), true},
		{"block country=CN", facts("/"), false},
		{"block client=headless,badbot", Facts{Client: "badbot"}, true},
		{"block client=headless", Facts{Client: "human"}, false},
		{"block score>=0.5", Facts{Score: 0.5}, true},
		{"block score>=0.5", Facts{Score: 0.49}, false},
		{"allow score<0.2", Facts{Score: 0.1}, true},
		{"allow header:X-Probe", facts("/"), true},
		{"allow header:X-Probe=ok", facts("/"), true},
		{"allow header:X-Probe=no", facts("/"), false},
		{"allow header:X-Other", facts("/"), false},
		{"block path^=/api method=GET", facts("/api/x"), true},
		{"block path^=/api method=POST", facts("/api/x"), false},
	} {
		s, bad := Parse(tc.rule)
		if len(bad) != 0 || len(s.Rules()) != 1 {
			t.Fatalf("%q: %v", tc.rule, bad)
		}
		if got := s.Match(tc.f, time.Now()) != nil; got != tc.match {
			t.Errorf("%q on %+v matched %v, want %v", tc.rule, tc.f, got, tc.match)
		}
	}
}

// The first acting rule that matches decides. A test rule and a log rule
// before it are counted and do not stop it, so adding one never changes what
// the rules after it do; a rule after the decision is not counted.
func TestTheFirstActingMatchDecidesAndEveryMatchIsCounted(t *testing.T) {
	s, bad := Parse("# notes are skipped\ntest block path^=/search\nlog path^=/search\nallow path^=/search\nblock path^=/")
	if len(bad) != 0 {
		t.Fatal(bad)
	}
	now := time.Unix(1_800_000_000, 0)
	r := s.Match(facts("/search?q=x"), now)
	if r == nil || r.Action != Allow || r.Line != 4 {
		t.Fatalf("matched %+v; want the allow on line 4", r)
	}
	for i, want := range []int64{1, 1, 1, 0} {
		n, at := s.Rules()[i].Hits()
		if n != want {
			t.Errorf("rule on line %d: %d hits, want %d", s.Rules()[i].Line, n, want)
		}
		if want == 1 && !at.Equal(now) {
			t.Errorf("rule on line %d last hit %v, want %v", s.Rules()[i].Line, at, now)
		}
	}
	if !s.Rules()[0].Test || s.Rules()[0].Action != Block {
		t.Error("the test rule lost its action or its mark")
	}
	if r := s.Match(Facts{Path: "/x"}, now); r == nil || r.Line != 5 {
		t.Errorf("a path no earlier rule names matched %+v; want line 5", r)
	}
}

// A line that cannot be read is named with its line and why, and the rest
// still compile; a rule with no condition, which would match the whole site,
// is refused.
func TestABadLineIsNamedAndTheRestStand(t *testing.T) {
	s, bad := Parse("block path^=/a\nrefuse path=/b\nblock\nblock score>=2\nblock client=robot\nchallenge path^=/c")
	if len(s.Rules()) != 2 {
		t.Errorf("%d rules compiled, want the 2 good ones", len(s.Rules()))
	}
	want := []string{"line 2:", "line 3: has no condition", "line 4:", "line 5:"}
	if len(bad) != len(want) {
		t.Fatalf("issues %v; want one per bad line", bad)
	}
	for i, w := range want {
		if !strings.HasPrefix(bad[i], w) {
			t.Errorf("issue %d is %q, want it to start %q", i, bad[i], w)
		}
	}
}

// No list of rules can grow the request path without bound.
func TestARuleSetIsCapped(t *testing.T) {
	s, bad := Parse(strings.Repeat("block path^=/x\n", MaxRules+5))
	if len(s.Rules()) != MaxRules || len(bad) != 1 {
		t.Errorf("%d rules, issues %v; want %d rules and one issue naming the cap", len(s.Rules()), bad, MaxRules)
	}
}

// Whatever an operator pastes, Parse never panics, never keeps more than
// MaxRules, never keeps a rule with no condition, and names every line it
// drops; and Match never panics on what it kept.
func FuzzParse(f *testing.F) {
	for _, s := range []string{"block path^=/wp-", "test challenge country=CN,RU score>=0.5", "allow header:X=1\nlog", "block score>=2", "block header:", "test"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, text string) {
		s, issues := Parse(text)
		if len(s.Rules()) > MaxRules {
			t.Fatalf("%d rules kept", len(s.Rules()))
		}
		for _, r := range s.Rules() {
			if len(r.conds) == 0 {
				t.Fatalf("line %d kept with no condition", r.Line)
			}
		}
		for _, is := range issues {
			if !strings.HasPrefix(is, "line ") {
				t.Fatalf("issue %q does not name its line", is)
			}
		}
		s.Match(Facts{Path: "/", Method: "GET"}, time.Now())
	})
}
