// SPDX-License-Identifier: Apache-2.0

// Package waf runs the OWASP Core Rule Set over the public site's requests,
// through Coraza.
//
// The rest of VayuShield decides who is asking; this decides whether what is
// asked is an attack: SQL and code injection, cross-site scripting, path
// traversal, protocol abuse, the shapes the Core Rule Set has been refined on
// for two decades. It is not where the product's defence against injection
// lives (that is parameterised queries, sanitised output and a strict CSP),
// and it is not a substitute for them: it is the layer that sees an attack
// on code this product did not write, a hosted site's own forms.
//
// Three modes. Off builds nothing. Detect records every match and refuses
// nothing, which is how a rule set is trusted on real traffic before it is
// let refuse it. Block answers 403 when the Core Rule Set's anomaly score
// passes its threshold.
//
// Only the request is read (phases 1 and 2): the response is this product's
// own output, and reading it would double the cost for nothing found.
package waf

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	coreruleset "github.com/corazawaf/coraza-coreruleset/v4"
	"github.com/corazawaf/coraza/v3"
	"github.com/corazawaf/coraza/v3/types"
)

// Mode is what the firewall does with a match.
type Mode string

const (
	ModeOff    Mode = "off"
	ModeDetect Mode = "detect"
	ModeBlock  Mode = "block"
)

// ParseMode reads a stored mode; anything else is off.
func ParseMode(s string) Mode {
	switch Mode(strings.TrimSpace(s)) {
	case ModeDetect:
		return ModeDetect
	case ModeBlock:
		return ModeBlock
	}
	return ModeOff
}

// bodyLimit is how much of a request body is read: a form or a JSON
// document, never an upload, which the routes that take one are left out
// of (Inspects).
const bodyLimit = 128 << 10

// keepRecent is how many matches the page lists.
const keepRecent = 200

// evaluationRule is the Core Rule Set's inbound anomaly evaluation: it
// matches when a request's score reaches the threshold, which is "would be
// refused" in detect mode and "was refused" in block mode.
const evaluationRule = 949110

// Match is one request a rule matched.
type Match struct {
	At      time.Time
	RuleID  int
	Message string
	Path    string
	Source  string // masked
	Refused bool   // reached the threshold (refused in block mode)
}

// RuleCount is how often one rule matched.
type RuleCount struct {
	RuleID  int
	Message string
	N       int
}

// Engine is the firewall. Its zero value is not usable; New makes one.
type Engine struct {
	mu       sync.RWMutex
	waf      coraza.WAF
	mode     Mode
	paranoia int

	inspected, matched, refused atomic.Int64

	recentMu sync.Mutex
	recent   []Match
	byRule   map[int]*RuleCount

	// Mask hides the last part of an address for the page; nil keeps none.
	Mask func(ip string) string
}

// New makes a firewall that is off.
func New() *Engine { return &Engine{mode: ModeOff, byRule: map[int]*RuleCount{}} }

// Configure sets the mode and paranoia level (1 to 4; the Core Rule Set's
// default is 1, the fewest false positives), building the rule set only
// when either changed. A failed build leaves the firewall as it was.
func (e *Engine) Configure(mode Mode, paranoia int) error {
	if paranoia < 1 || paranoia > 4 {
		paranoia = 1
	}
	e.mu.RLock()
	same := e.mode == mode && (mode == ModeOff || e.paranoia == paranoia)
	e.mu.RUnlock()
	if same {
		return nil
	}
	if mode == ModeOff {
		e.mu.Lock()
		e.waf, e.mode = nil, ModeOff
		e.mu.Unlock()
		return nil
	}
	w, err := build(mode, paranoia)
	if err != nil {
		return err
	}
	e.mu.Lock()
	e.waf, e.mode, e.paranoia = w, mode, paranoia
	e.mu.Unlock()
	return nil
}

func build(mode Mode, paranoia int) (coraza.WAF, error) {
	engine := "DetectionOnly"
	if mode == ModeBlock {
		engine = "On"
	}
	directives := fmt.Sprintf(`Include @coraza.conf-recommended
SecRuleEngine %s
SecRequestBodyAccess On
SecRequestBodyLimit %d
SecRequestBodyInMemoryLimit %d
SecRequestBodyNoFilesLimit %d
SecRequestBodyLimitAction ProcessPartial
SecResponseBodyAccess Off
SecAuditEngine Off
Include @crs-setup.conf.example
SecAction "id:900000,phase:1,pass,nolog,t:none,setvar:tx.blocking_paranoia_level=%d"
Include @owasp_crs/*.conf
`, engine, bodyLimit, bodyLimit, bodyLimit, paranoia)
	return coraza.NewWAF(coraza.NewWAFConfig().WithDirectives(directives).WithRootFS(coreruleset.FS))
}

// Mode is the firewall's mode now.
func (e *Engine) Mode() Mode {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.mode
}

// Paranoia is its paranoia level now.
func (e *Engine) Paranoia() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.paranoia
}

// Inspect reads r's request line, headers and (a form or JSON) body against
// the rule set, records what matched, and reports whether r is refused: only
// in block mode. The body is handed back to r unchanged.
func (e *Engine) Inspect(r *http.Request, clientIP string) (refused bool) {
	e.mu.RLock()
	w, mode := e.waf, e.mode
	e.mu.RUnlock()
	if w == nil || plainRead(r) {
		return false
	}
	e.inspected.Add(1)
	tx := w.NewTransaction()
	defer func() {
		tx.ProcessLogging()
		_ = tx.Close()
	}()
	tx.ProcessConnection(clientIP, 0, "", 0)
	tx.ProcessURI(r.URL.RequestURI(), r.Method, r.Proto)
	for k, vs := range r.Header {
		for _, v := range vs {
			tx.AddRequestHeader(k, v)
		}
	}
	if r.Host != "" {
		tx.AddRequestHeader("Host", r.Host)
		tx.SetServerName(r.Host)
	}
	// Go's server keeps the framing out of Header (ContentLength,
	// TransferEncoding); the rule set reads it as headers, and without them
	// every POST is "without Content-Length".
	if r.ContentLength > 0 && r.Header.Get("Content-Length") == "" {
		tx.AddRequestHeader("Content-Length", strconv.FormatInt(r.ContentLength, 10))
	}
	for _, te := range r.TransferEncoding {
		tx.AddRequestHeader("Transfer-Encoding", te)
	}
	it := tx.ProcessRequestHeaders()
	if it == nil && r.Body != nil && r.Body != http.NoBody && readsBody(r) {
		if buf, err := io.ReadAll(io.LimitReader(r.Body, bodyLimit)); err == nil {
			r.Body = struct {
				io.Reader
				io.Closer
			}{io.MultiReader(bytes.NewReader(buf), r.Body), r.Body}
			if it2, _, err := tx.WriteRequestBody(buf); err == nil && it2 != nil {
				it = it2
			}
		}
	}
	// Phase 2 runs for every request, body or none: it is where the rule
	// set reads the arguments for injection and scores the request.
	if it == nil {
		if it2, err := tx.ProcessRequestBody(); err == nil {
			it = it2
		}
	}
	reached := it != nil
	matches := make([]types.MatchedRule, 0, 4)
	for _, m := range tx.MatchedRules() {
		id := m.Rule().ID()
		if id == evaluationRule {
			reached = true
			continue
		}
		if id < 911000 || id >= 949000 || m.Message() == "" {
			continue // setup and scoring rules, not findings
		}
		matches = append(matches, m)
	}
	if len(matches) == 0 && !reached {
		return false
	}
	refused = reached && mode == ModeBlock
	e.matched.Add(1)
	if refused {
		e.refused.Add(1)
	}
	e.record(r, clientIP, matches, reached)
	return refused
}

// plainRead is a request with nothing in it for the rule set to read: a GET
// or HEAD of a clean path (letters, digits, "/", "-", "_", "." and no "..")
// with no query string, no body, and no header carrying an injection's
// marker. A reader's page view is this, and the full rule set costs about
// 0.8 ms of CPU a request (BenchmarkAnOrdinaryPage), several times a cached
// page; everything else, a query, a form, a traversal, a Log4Shell or
// Shellshock header, is read in full.
func plainRead(r *http.Request) bool {
	if (r.Method != http.MethodGet && r.Method != http.MethodHead) || r.URL.RawQuery != "" || r.ContentLength > 0 || len(r.TransferEncoding) > 0 {
		return false
	}
	p := r.URL.EscapedPath()
	if strings.Contains(p, "..") {
		return false
	}
	for i := 0; i < len(p); i++ {
		c := p[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '/' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	for _, vs := range r.Header {
		for _, v := range vs {
			if strings.Contains(v, "${") || strings.Contains(v, "() {") || strings.Contains(v, "<") ||
				strings.Contains(v, "%{") || strings.IndexByte(v, 0) >= 0 {
				return false
			}
		}
	}
	return true
}

// readsBody is whether r's body is a form or JSON document, the kinds the
// rule set reads; anything else (an upload, a binary) is left alone.
func readsBody(r *http.Request) bool {
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	return strings.HasPrefix(ct, "application/x-www-form-urlencoded") ||
		strings.HasPrefix(ct, "application/json") || strings.HasPrefix(ct, "text/plain")
}

func (e *Engine) record(r *http.Request, ip string, matches []types.MatchedRule, reached bool) {
	src := ip
	if e.Mask != nil {
		src = e.Mask(ip)
	}
	path := r.URL.Path
	if len(path) > 200 {
		path = path[:200]
	}
	now := time.Now()
	e.recentMu.Lock()
	defer e.recentMu.Unlock()
	for _, m := range matches {
		id := m.Rule().ID()
		c := e.byRule[id]
		if c == nil {
			c = &RuleCount{RuleID: id, Message: m.Message()}
			e.byRule[id] = c
		}
		c.N++
	}
	first := Match{At: now, Path: path, Source: src, Refused: reached}
	if len(matches) > 0 {
		first.RuleID, first.Message = matches[0].Rule().ID(), matches[0].Message()
	}
	e.recent = append(e.recent, first)
	if len(e.recent) > keepRecent {
		e.recent = append([]Match(nil), e.recent[len(e.recent)-keepRecent:]...)
	}
}

// Counts is how many requests were inspected, matched a rule, and were
// refused, since the last restart.
func (e *Engine) Counts() (inspected, matched, refused int64) {
	return e.inspected.Load(), e.matched.Load(), e.refused.Load()
}

// Recent is the latest matches, newest first.
func (e *Engine) Recent() []Match {
	e.recentMu.Lock()
	defer e.recentMu.Unlock()
	out := make([]Match, len(e.recent))
	for i, m := range e.recent {
		out[len(e.recent)-1-i] = m
	}
	return out
}

// Rules is how often each rule matched, most first.
func (e *Engine) Rules() []RuleCount {
	e.recentMu.Lock()
	defer e.recentMu.Unlock()
	out := make([]RuleCount, 0, len(e.byRule))
	for _, c := range e.byRule {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].N != out[j].N {
			return out[i].N > out[j].N
		}
		return out[i].RuleID < out[j].RuleID
	})
	return out
}
