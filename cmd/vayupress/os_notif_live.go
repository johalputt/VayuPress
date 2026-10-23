// SPDX-License-Identifier: Apache-2.0

package main

// os_notif_live.go — the bell, live. The bell is drawn into the shell when a
// page renders, so a page left open showed what was true when it loaded: a
// comment, a message or new mail arrived and nothing moved until the operator
// went to another page. Now each open console page holds one stream, and the
// server redraws that viewer's bell and sends it whenever it changed.
//
// What wakes a redraw:
//   - a pulse: mail filed into a mailbox, a post created, changed or deleted,
//     the bell marked read or cleared (in any tab), or the change watcher
//     seeing one of the bell's tables move;
//   - a slow tick, for the conditions read from nowhere the watcher looks: a
//     backup verdict, a mail domain's DNS, a hosted site's checks.
//
// The watcher runs only while a stream is open and reads a few counts every
// two seconds, each one an index lookup, never the bell itself. A bell is
// redrawn only on a pulse or the tick, the same work as one page view, and
// sent only when it differs from what the page already shows.

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/johalputt/vayupress/internal/auth"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/mode"
)

const (
	// bellWatchEvery is how often the watcher reads its counts while a bell
	// is open: the longest a comment, a message or a new member waits to show.
	bellWatchEvery = 2 * time.Second
	// bellTickEvery redraws the bell even when nothing pulsed, for the
	// conditions no count follows.
	bellTickEvery = 60 * time.Second
	// bellSettle gathers a burst of pulses (a bulk import, a batch of mail)
	// into one redraw.
	bellSettle = 300 * time.Millisecond
	// bellStreamsPerUser bounds one account's open bells. A tab past it is
	// refused and keeps the bell its page drew.
	bellStreamsPerUser = 12
	bellStreamsMax     = 200
	// bellStreamLife ends a stream so the browser opens a new one, through
	// sign-in again: a role changed since is drawn by its new level.
	bellStreamLife = 10 * time.Minute
)

// bellPulse tells every open bell to redraw. The zero value is ready.
type bellPulse struct {
	mu     sync.Mutex
	subs   map[chan struct{}]string // channel → user id
	perUsr map[string]int
	stop   chan struct{} // closes the running watcher; nil while none runs
	// watch is the change watcher, started with the first stream and stopped
	// with the last. Nil leaves only the pulses and the tick (tests).
	watch func(stop <-chan struct{})
}

// subscribe opens a bell for userID. ok is false when the user, or the
// install, already has as many open as it may.
func (p *bellPulse) subscribe(userID string) (ch <-chan struct{}, cancel func(), ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.subs == nil {
		p.subs, p.perUsr = map[chan struct{}]string{}, map[string]int{}
	}
	if len(p.subs) >= bellStreamsMax || p.perUsr[userID] >= bellStreamsPerUser {
		return nil, nil, false
	}
	c := make(chan struct{}, 1)
	p.subs[c] = userID
	p.perUsr[userID]++
	if p.stop == nil && p.watch != nil {
		p.stop = make(chan struct{})
		go p.watch(p.stop)
	}
	var once sync.Once
	return c, func() { once.Do(func() { p.unsubscribe(c) }) }, true
}

func (p *bellPulse) unsubscribe(c chan struct{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	u, ok := p.subs[c]
	if !ok {
		return
	}
	delete(p.subs, c)
	if p.perUsr[u]--; p.perUsr[u] <= 0 {
		delete(p.perUsr, u)
	}
	if len(p.subs) == 0 && p.stop != nil {
		close(p.stop)
		p.stop = nil
	}
}

// fire wakes every open bell. It never blocks: a bell already woken and not
// yet redrawn will redraw once, which is all it needs.
func (p *bellPulse) fire() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for c := range p.subs {
		select {
		case c <- struct{}{}:
		default:
		}
	}
}

// open is how many bells are streaming.
func (p *bellPulse) open() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.subs)
}

// bellWatchSQL are the counts the watcher follows: the tables the bell reads,
// each as its newest row and the tally the bell shows. MAX(rowid) is the
// table's last key, and the tallies are the bell's own indexed counts. Each
// runs alone, so a table missing on an old schema blanks only its own line.
var bellWatchSQL = []string{
	`SELECT COALESCE(MAX(rowid),0) || '/' || (SELECT COUNT(1) FROM contact_messages WHERE is_read=0) FROM contact_messages`,
	`SELECT COALESCE(MAX(rowid),0) || '/' || (SELECT COUNT(1) FROM comments WHERE status='pending') FROM comments`,
	`SELECT COALESCE(MAX(rowid),0) FROM members`,
	`SELECT COALESCE(MAX(rowid),0) FROM articles`,
	`SELECT COUNT(1) FROM update_history WHERE completed_at IS NOT NULL`,
	`SELECT COUNT(1) FROM vayumail_app_passwords WHERE status='pending'`,
}

// bellDigest is the watcher's reading: it changes when anything the bell
// counts does, and says nothing about what.
func (a *App) bellDigest(ctx context.Context) string {
	var b strings.Builder
	b.WriteString(strconv.Itoa(len(mode.Global.History())))
	b.WriteByte(';')
	b.WriteString(strconv.FormatInt(a.vayuKeepStatus().LastSuccess.Unix(), 10))
	if a.vayuMail != nil && a.vayuMail.Accounts() != nil {
		b.WriteByte(';')
		b.WriteString(strconv.Itoa(a.vayuMail.Accounts().PendingRecoveryCount(ctx)))
	}
	if dbpkg.DB == nil {
		return b.String()
	}
	rdb := dbpkg.Reader()
	for _, q := range bellWatchSQL {
		var v sql.NullString
		_ = rdb.QueryRowContext(ctx, q).Scan(&v)
		b.WriteByte(';')
		b.WriteString(v.String)
	}
	return b.String()
}

// watchBell pulses the bells whenever bellDigest moves, until stop closes.
func (a *App) watchBell(stop <-chan struct{}) {
	t := time.NewTicker(bellWatchEvery)
	defer t.Stop()
	read := func() string {
		ctx, cancel := context.WithTimeout(context.Background(), bellWatchEvery)
		defer cancel()
		return a.bellDigest(ctx)
	}
	last := read()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			if d := read(); d != last {
				last = d
				a.bellLive.fire()
			}
		}
	}
}

// bellOut is one redrawn bell: the element whole, and its signature.
type bellOut struct {
	HTML string `json:"html"`
	Sig  string `json:"sig"`
}

// handleOSNotificationsStream streams the viewer's bell as it changes. The
// page names the signature of the bell it drew (?sig=), so a bell that has
// not changed since is never sent; one that has is sent at once on connect.
// Each redraw is the shell's own (getOSSettings, osNotifBell), so the live
// bell and a freshly loaded page's bell can never disagree.
func (a *App) handleOSNotificationsStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	a.bellLive.mu.Lock()
	if a.bellLive.watch == nil {
		a.bellLive.watch = a.watchBell
	}
	a.bellLive.mu.Unlock()
	pulse, cancel, ok := a.bellLive.subscribe(currentUserIDOf(r))
	if !ok {
		http.Error(w, "too many open bells", http.StatusTooManyRequests)
		return
	}
	defer cancel()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	rc := http.NewResponseController(w)
	ctx := r.Context()
	sent := r.URL.Query().Get("sig")
	token := auth.SessionTokenFromRequest(r)
	send := func() bool {
		// Signed out in another tab: the stream ends with the session, and
		// never draws a bell for a sign-in that is gone.
		if token != "" && a.sessions != nil {
			if _, err := a.sessions.Validate(ctx, token); err != nil {
				return false
			}
		}
		bell, sig := osNotifBellSig(a.getOSSettings(ctx))
		if sig == sent {
			return true
		}
		if !writeSSE(w, rc, "bell", bellOut{HTML: bell, Sig: sig}) {
			return false
		}
		flusher.Flush()
		sent = sig
		return true
	}
	if !send() {
		return
	}

	life := time.NewTimer(bellStreamLife)
	defer life.Stop()
	tick := time.NewTicker(bellTickEvery)
	defer tick.Stop()
	ping := time.NewTicker(streamHeartbeat)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-life.C:
			return
		case <-pulse:
			// Let a burst settle, then draw once.
			select {
			case <-ctx.Done():
				return
			case <-time.After(bellSettle):
			}
			if !send() {
				return
			}
		case <-tick.C:
			if !send() {
				return
			}
		case <-ping.C:
			if !writeSSE(w, rc, "ping", struct{}{}) {
				return
			}
			flusher.Flush()
		}
	}
}
