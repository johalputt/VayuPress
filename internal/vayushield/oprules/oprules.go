// SPDX-License-Identifier: Apache-2.0

// Package oprules is the operator's own request rules: what a request asks
// for and who is asking, decided by the operator in plain lines.
//
//	block path^=/wp-
//	challenge country=CN,RU path^=/search
//	allow header:X-Health-Check=ok
//	test block client=headless
//	log score>=0.6 method=POST
//	limit 20/min path^=/search
//
// A line is an action, then conditions that must all hold. The first rule in
// order that matches and acts decides. "test" in front runs a rule without
// acting on it: it is counted as what it would have done, so a rule can be
// watched against real traffic before it is trusted. A test rule and a log
// rule are counted and evaluation goes on, so adding one never changes what
// the rules after it do.
//
// "limit N/min" (or N/hour) gives each source its own allowance on the
// requests the rule matches: N at once, refilling evenly over the period. A
// source within it goes on to the next rule; one past it is refused with a
// 429 and when to come back. The allowance is held per rule, in memory, and
// starts afresh when the rules are saved.
//
// This is not a WAF and does not claim to be one. Injection defence in
// VayuPress is structural (parameterised queries, sanitising, the CSP); these
// rules are facts an operator states about their own traffic, evaluated in a
// few comparisons per rule. A rule set is capped in size so that no list of
// rules can make the request path slow.
package oprules

import (
	"math"
	"path"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/johalputt/vayupress/internal/vayushield/resilience"
)

// Action is what a matching rule does.
type Action int

const (
	// Block refuses the request.
	Block Action = iota + 1
	// Challenge asks for a proof of work.
	Challenge
	// Allow serves the request without the shield's challenge ladder.
	Allow
	// Log only counts the request.
	Log
	// Limit refuses a source past its allowance on the matching requests.
	Limit
)

func (a Action) String() string {
	switch a {
	case Block:
		return "block"
	case Challenge:
		return "challenge"
	case Allow:
		return "allow"
	case Log:
		return "log"
	case Limit:
		return "limit"
	}
	return ""
}

// MaxRules is the most rules a set holds; lines past it are refused, by name.
const MaxRules = 100

// MaxConditions is the most conditions a rule holds. With MaxRules it is
// what bounds a request's cost: 100 rules of 16 conditions, each tested, is
// the most any request pays (15 µs on four cores,
// BenchmarkMatchTheCostliestSetThatMisses), where the 32 KiB a save takes
// held 4,000 conditions, 33 µs. A rule has eight things to test.
const MaxConditions = 16

// limitKeys bounds the sources one limit rule tracks, so a hundred of them
// over a spray of addresses stays a few tens of megabytes; past it the
// limiter forgets idle sources first.
const limitKeys = 4096

// Facts are what a rule can test about a request.
type Facts struct {
	Path, Method, Country string
	Client                string  // the shield's client type, lowercase: human, goodbot, badbot, aiagent, headless, unknown
	Score                 float64 // the shield's bot score
	Header                func(name string) string
	// Source is who a limit is counted against. Empty, a limit rule matches
	// and counts but never refuses: there is no one to hold it against.
	Source string
}

// Rule is one compiled line.
type Rule struct {
	Line   int
	Text   string
	Action Action
	Test   bool
	// RetryAfter is, for a limit rule, the seconds until a refused source has
	// one request again.
	RetryAfter int

	conds   []cond
	hits    atomic.Int64
	lastHit atomic.Int64 // unix seconds
	limiter *resilience.Limiter
	refused atomic.Int64
	// unseen is set when a condition tests something a recorded page view
	// does not keep (the score, a header), so Would cannot judge the rule.
	unseen bool
}

// Judgeable reports whether Would can decide this rule from the facts a
// recorded page view keeps: its path, method, country and client.
func (r *Rule) Judgeable() bool { return !r.unseen }

// Hits is how many requests the rule has matched since it was compiled, and
// when it last did (zero when never).
func (r *Rule) Hits() (int64, time.Time) {
	n := r.hits.Load()
	if at := r.lastHit.Load(); at > 0 {
		return n, time.Unix(at, 0)
	}
	return n, time.Time{}
}

// Would is the rule that would decide f if every rule acted, test rules
// included, counting nothing. Log and limit rules never decide here (a limit
// depends on a source's pace, which a record does not keep), nor do rules
// that are not Judgeable.
func (s *Set) Would(f Facts) *Rule {
	if s == nil {
		return nil
	}
	for _, r := range s.rules {
		if r.Action == Log || r.Action == Limit || r.unseen {
			continue
		}
		if r.matches(f) {
			return r
		}
	}
	return nil
}

// Refused is how many requests a limit rule has refused (for a test rule,
// would have refused) since it was compiled.
func (r *Rule) Refused() int64 { return r.refused.Load() }

// Set is a compiled rule list. The nil Set matches nothing.
type Set struct {
	rules []*Rule
}

// Rules is the set's rules in order.
func (s *Set) Rules() []*Rule {
	if s == nil {
		return nil
	}
	return s.rules
}

// Match is the first acting rule that matches f; nil when none does. Every
// matching rule up to it is counted, test and log rules included.
func (s *Set) Match(f Facts, now time.Time) *Rule {
	if s == nil {
		return nil
	}
	for _, r := range s.rules {
		if !r.matches(f) {
			continue
		}
		r.hits.Add(1)
		r.lastHit.Store(now.Unix())
		switch {
		case r.Action == Limit:
			if f.Source == "" || r.limiter.Allow(f.Source) {
				continue
			}
			r.refused.Add(1)
			if !r.Test {
				return r
			}
		case !r.Test && r.Action != Log:
			return r
		}
	}
	return nil
}

// matches reports whether the rule holds for the request, by every reading
// of its path a handler might serve. The path is matched as it arrived and
// as path.Clean makes it ("//p", "/./p", "/x/../p" and "/p/" are all "/p"),
// since a handler that cleans it serves the cleaned page: an uploaded site's
// file server did, so "block path=/p.html" was walked around by "/p.html/".
// A refusal (block, challenge, limit, log) holds when either reading
// matches; an allow only when both do, so it cannot be widened by a path
// that reads as allowed and is served as something else.
func (r *Rule) matches(f Facts) bool {
	clean := cleanPath(f.Path)
	if clean == f.Path {
		return r.matchesAs(f)
	}
	raw := r.matchesAs(f)
	if raw && r.Action != Allow {
		return true
	}
	if !raw && r.Action == Allow {
		return false
	}
	f.Path = clean
	return r.matchesAs(f)
}

// cleanPath is p as path.Clean reads it from the root.
func cleanPath(p string) string {
	if p == "" {
		return p
	}
	return path.Clean("/" + p)
}

func (r *Rule) matchesAs(f Facts) bool {
	for _, c := range r.conds {
		if !c(f) {
			return false
		}
	}
	return true
}

type cond func(Facts) bool

// Parse compiles text, one rule per line; a line starting with # is a note.
// A line that cannot be read is skipped and named in issues, with its line
// number, so one typo never takes the other rules with it.
func Parse(text string) (*Set, []string) {
	s := &Set{}
	var issues []string
	for i, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		n := i + 1
		if len(s.rules) == MaxRules {
			issues = append(issues, "line "+strconv.Itoa(n)+": more than "+strconv.Itoa(MaxRules)+" rules; this and the rest are ignored")
			break
		}
		r, err := parseLine(line)
		if err != "" {
			issues = append(issues, "line "+strconv.Itoa(n)+": "+err)
			continue
		}
		r.Line, r.Text = n, line
		s.rules = append(s.rules, r)
	}
	return s, issues
}

func parseLine(line string) (*Rule, string) {
	words := strings.Fields(line)
	r := &Rule{}
	if strings.EqualFold(words[0], "test") {
		r.Test = true
		words = words[1:]
		if len(words) == 0 {
			return nil, "test of what? give an action after it"
		}
	}
	switch strings.ToLower(words[0]) {
	case "block":
		r.Action = Block
	case "challenge":
		r.Action = Challenge
	case "allow":
		r.Action = Allow
	case "log":
		r.Action = Log
	case "limit":
		r.Action = Limit
		if len(words) < 2 {
			return nil, "limit needs an allowance after it, as limit 20/min"
		}
		if err := r.setLimit(words[1]); err != "" {
			return nil, err
		}
		words = append(words[:1:1], words[2:]...)
	default:
		return nil, "starts with " + strconv.Quote(words[0]) + "; a rule starts with block, challenge, allow, log or limit"
	}
	if len(words) == 1 {
		// A rule with no condition matches everything; that is the whole site,
		// and is far more likely a slip than an intent.
		return nil, "has no condition; a rule that matches every request is not accepted"
	}
	if len(words)-1 > MaxConditions {
		return nil, "has " + strconv.Itoa(len(words)-1) + " conditions; a rule holds at most " + strconv.Itoa(MaxConditions)
	}
	for _, w := range words[1:] {
		c, err := parseCond(w)
		if err != "" {
			return nil, err
		}
		r.conds = append(r.conds, c)
		if lw := strings.ToLower(w); strings.HasPrefix(lw, "score") || strings.HasPrefix(lw, "header:") {
			r.unseen = true
		}
	}
	return r, ""
}

// setLimit reads "N/min" or "N/hour": N requests at once, refilling evenly
// over the period.
func (r *Rule) setLimit(w string) string {
	n, per, ok := strings.Cut(strings.ToLower(w), "/")
	count, err := strconv.Atoi(n)
	if !ok || err != nil || count < 1 || count > 100000 {
		return strconv.Quote(w) + ": an allowance is a count and a period, as 20/min or 500/hour"
	}
	var period float64
	switch per {
	case "min", "minute":
		period = 60
	case "hour", "h":
		period = 3600
	default:
		return strconv.Quote(w) + ": the period is min or hour"
	}
	r.limiter = resilience.NewLimiter(float64(count)/period, float64(count), limitKeys)
	r.RetryAfter = int(math.Ceil(period / float64(count)))
	return ""
}

func parseCond(w string) (cond, string) {
	lw := strings.ToLower(w)
	switch {
	case strings.HasPrefix(lw, "path^="):
		v := w[len("path^="):]
		return func(f Facts) bool { return strings.HasPrefix(f.Path, v) }, nonEmpty(v, w)
	case strings.HasPrefix(lw, "path*="):
		v := w[len("path*="):]
		return func(f Facts) bool { return strings.Contains(f.Path, v) }, nonEmpty(v, w)
	case strings.HasPrefix(lw, "path="):
		v := w[len("path="):]
		return func(f Facts) bool { return f.Path == v }, nonEmpty(v, w)
	case strings.HasPrefix(lw, "method="):
		set := upperSet(w[len("method="):])
		return func(f Facts) bool { return set[strings.ToUpper(f.Method)] }, nonEmptySet(set, w)
	case strings.HasPrefix(lw, "country="):
		set := upperSet(w[len("country="):])
		return func(f Facts) bool { return set[strings.ToUpper(f.Country)] }, nonEmptySet(set, w)
	case strings.HasPrefix(lw, "client="):
		set := map[string]bool{}
		for _, c := range strings.Split(strings.ToLower(w[len("client="):]), ",") {
			switch c {
			case "human", "goodbot", "badbot", "aiagent", "headless", "unknown":
				set[c] = true
			default:
				return nil, strconv.Quote(c) + " is not a client type: human, goodbot, badbot, aiagent, headless or unknown"
			}
		}
		return func(f Facts) bool { return set[f.Client] }, ""
	case strings.HasPrefix(lw, "score>="):
		n, err := strconv.ParseFloat(w[len("score>="):], 64)
		if err != nil || n < 0 || n > 1 {
			return nil, strconv.Quote(w) + ": a score is a number from 0 to 1"
		}
		return func(f Facts) bool { return f.Score >= n }, ""
	case strings.HasPrefix(lw, "score<"):
		n, err := strconv.ParseFloat(w[len("score<"):], 64)
		if err != nil || n < 0 || n > 1 {
			return nil, strconv.Quote(w) + ": a score is a number from 0 to 1"
		}
		return func(f Facts) bool { return f.Score < n }, ""
	case strings.HasPrefix(lw, "header:"):
		spec := w[len("header:"):]
		name, val, hasVal := strings.Cut(spec, "=")
		if name == "" {
			return nil, strconv.Quote(w) + ": name the header, as header:Name or header:Name=value"
		}
		if !hasVal {
			return func(f Facts) bool { return f.Header != nil && f.Header(name) != "" }, ""
		}
		return func(f Facts) bool { return f.Header != nil && f.Header(name) == val }, ""
	}
	return nil, strconv.Quote(w) + " is not a condition: path=, path^=, path*=, method=, country=, client=, score>=, score< or header:"
}

func nonEmpty(v, w string) string {
	if v == "" {
		return strconv.Quote(w) + " has nothing after the ="
	}
	return ""
}

func nonEmptySet(set map[string]bool, w string) string {
	if len(set) == 0 {
		return strconv.Quote(w) + " has nothing after the ="
	}
	return ""
}

func upperSet(list string) map[string]bool {
	set := map[string]bool{}
	for _, v := range strings.Split(list, ",") {
		if v = strings.ToUpper(strings.TrimSpace(v)); v != "" {
			set[v] = true
		}
	}
	return set
}
