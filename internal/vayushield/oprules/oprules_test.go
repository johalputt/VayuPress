// SPDX-License-Identifier: Apache-2.0

package oprules

import (
	"strconv"
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
	for _, s := range []string{"block path^=/wp-", "test challenge country=CN,RU score>=0.5", "allow header:X=1\nlog", "block score>=2", "block header:", "test", "limit 3/min path=/x", "test limit 1/hour method=POST", "limit 0/s"} {
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

// A limit holds each source to its own allowance on the requests it matches,
// and a source within it goes on to the rules after.
func TestALimitHoldsEachSourceToItsAllowance(t *testing.T) {
	s, bad := Parse("limit 2/min path^=/search\nblock path=/search/x")
	if len(bad) != 0 {
		t.Fatal(bad)
	}
	lim := s.Rules()[0]
	if lim.Action != Limit || lim.RetryAfter != 30 {
		t.Fatalf("parsed %v, retry after %d; want a limit, 30 s", lim.Action, lim.RetryAfter)
	}
	at := func(path, src string) *Rule { return s.Match(Facts{Path: path, Source: src}, time.Now()) }
	if r := at("/search", "a"); r != nil {
		t.Fatalf("the first request was decided by %+v", r)
	}
	if r := at("/search/x", "a"); r == nil || r.Line != 2 {
		t.Fatalf("a request within the allowance did not reach the next rule: %+v", r)
	}
	if r := at("/search", "a"); r != lim {
		t.Fatalf("the third request in a minute was decided by %+v; want the limit", r)
	}
	if r := at("/search", "b"); r != nil {
		t.Error("another source was held to the first one's allowance")
	}
	if lim.Refused() != 1 {
		t.Errorf("refused %d, want 1", lim.Refused())
	}
	if r := at("/search", ""); r != nil {
		t.Error("a request with no source was refused: there is no one to hold the limit against")
	}
}

// A test limit counts what it would have refused and refuses nothing.
func TestATestLimitCountsAndDoesNotRefuse(t *testing.T) {
	s, _ := Parse("test limit 1/hour path=/contact")
	for i := 0; i < 3; i++ {
		if r := s.Match(Facts{Path: "/contact", Source: "a"}, time.Now()); r != nil {
			t.Fatalf("a test limit refused: %+v", r)
		}
	}
	if got := s.Rules()[0].Refused(); got != 2 {
		t.Errorf("would have refused %d, want 2", got)
	}
}

// An allowance that cannot be read, one per reason, is named.
func TestABadAllowanceIsNamed(t *testing.T) {
	for line, want := range map[string]string{
		"limit path=/x":       "a count and a period",
		"limit 0/min path=/x": "a count and a period",
		"limit 5/day path=/x": "the period is min or hour",
		"limit 5/min":         "has no condition",
		"limit":               "needs an allowance",
	} {
		s, bad := Parse(line)
		if len(s.Rules()) != 0 || len(bad) != 1 || !strings.Contains(bad[0], want) {
			t.Errorf("%q: kept %d, issues %v; want it refused for %q", line, len(s.Rules()), bad, want)
		}
	}
}

// A full set, none of which matches: the most a request can pay.
func BenchmarkMatchAFullSetThatMisses(b *testing.B) {
	var lines []string
	for i := 0; i < MaxRules; i++ {
		lines = append(lines, "limit 20/min method=POST path^=/r"+strconv.Itoa(i)+" header:X-Probe=no")
	}
	s, _ := Parse(strings.Join(lines, "\n"))
	f := facts("/article/a-long-enough-slug")
	f.Source = "198.51.100.7"
	now := time.Now()
	b.ReportAllocs()
	for b.Loop() {
		s.Match(f, now)
	}
}

// Would judges a draft as if every rule acted, test rules included, counts
// nothing, and leaves out what a recorded view cannot answer: a log, a
// limit, a score or a header.
func TestWouldJudgesWithoutCounting(t *testing.T) {
	s, bad := Parse("log path^=/\nlimit 1/min path^=/\nblock score<0.5 path^=/a\nallow header:X-Probe path^=/a\ntest block path^=/a\nchallenge country=IN")
	if len(bad) != 0 {
		t.Fatal(bad)
	}
	f := facts("/a/b")
	if r := s.Would(f); r == nil || r.Line != 5 {
		t.Fatalf("would %+v; want the test block on line 5", r)
	}
	if r := s.Would(facts("/x")); r == nil || r.Line != 6 {
		t.Errorf("would %+v; want the challenge on line 6", r)
	}
	for _, r := range s.Rules() {
		if n, _ := r.Hits(); n != 0 {
			t.Errorf("line %d counted %d hits from Would", r.Line, n)
		}
	}
	for i, want := range []bool{true, true, false, false, true, true} {
		if got := s.Rules()[i].Judgeable(); got != want {
			t.Errorf("line %d judgeable %v, want %v", i+1, got, want)
		}
	}
}
