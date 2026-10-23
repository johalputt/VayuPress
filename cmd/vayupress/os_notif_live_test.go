// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// The bell, live (os_notif_live.go): a page left open sees what arrives
// without going to another page.

func TestTheBellCarriesItsOwnSignature(t *testing.T) {
	bell, sig := osNotifBellSig(bellFixture(time.Time{}))
	if sig == "" || !strings.Contains(bell, `data-notif-sig="`+sig+`"`) {
		t.Fatalf("the bell does not carry the signature %q:\n%s", sig, bell)
	}
	if _, again := osNotifBellSig(bellFixture(time.Time{})); again != sig {
		t.Errorf("the same bell drew two signatures: %q, %q", sig, again)
	}
	read := bellFixture(time.Now())
	if _, other := osNotifBellSig(read); other == sig {
		t.Error("a bell whose events were read kept the unread bell's signature")
	}
}

// bellStream opens the stream against a running server and returns its
// events, one per receive, until the test ends.
func bellStream(t *testing.T, a *App, sig string) <-chan bellOut {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(a.handleOSNotificationsStream))
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/os/api/notifications/stream?sig="+sig, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("the stream answered %d %q", res.StatusCode, ct)
	}
	t.Cleanup(func() { cancel(); _ = res.Body.Close(); srv.Close() })
	out := make(chan bellOut, 8)
	go func() {
		sc := bufio.NewScanner(res.Body)
		sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
		event := ""
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: ") && event == "bell":
				var b bellOut
				if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &b) == nil {
					out <- b
				}
			}
		}
	}()
	return out
}

func TestTheStreamSendsTheBellWhenItMovesAndOnlyThen(t *testing.T) {
	openMigratedDB(t)
	a := &App{}
	_, sig := osNotifBellSig(a.getOSSettings(context.Background()))

	// The page's bell is current: nothing is sent on connect.
	events := bellStream(t, a, sig)
	select {
	case b := <-events:
		t.Fatalf("an unchanged bell was sent on connect:\n%s", b.HTML)
	case <-time.After(bellSettle + 200*time.Millisecond):
	}

	// A message arrives: the watcher sees its table move, and the bell is
	// redrawn and sent with it on, without anything else nudging it.
	if _, err := dbpkg.DB.Exec(`INSERT INTO contact_messages(id,name,email,message,created_at) VALUES('m1','Priya','p@example.net','hi',?)`, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	select {
	case b := <-events:
		if !strings.Contains(b.HTML, "Message from Priya") || !strings.Contains(b.HTML, "data-notif-badge") {
			t.Errorf("the redrawn bell does not show the message:\n%s", b.HTML)
		}
		if b.Sig == sig || !strings.Contains(b.HTML, `data-notif-sig="`+b.Sig+`"`) {
			t.Errorf("the redrawn bell's signature %q is stale or not its own", b.Sig)
		}
	case <-time.After(bellWatchEvery + bellSettle + 3*time.Second):
		t.Fatal("a new message never reached the open bell")
	}

	// A pulse with nothing new sends nothing.
	a.bellLive.fire()
	select {
	case b := <-events:
		t.Errorf("a pulse with nothing new resent the bell:\n%s", b.HTML)
	case <-time.After(bellSettle + 300*time.Millisecond):
	}
}

func TestAStalePageGetsTheBellOnConnect(t *testing.T) {
	openMigratedDB(t)
	a := &App{}
	select {
	case b := <-bellStream(t, a, "stale"):
		if b.Sig == "" || b.HTML == "" {
			t.Errorf("an empty bell was sent: %+v", b)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a page showing an old bell was not sent the current one")
	}
}

func TestTheWatcherRunsOnlyWhileABellIsOpen(t *testing.T) {
	started, stopped := make(chan struct{}, 4), make(chan struct{}, 4)
	p := &bellPulse{watch: func(stop <-chan struct{}) {
		started <- struct{}{}
		<-stop
		stopped <- struct{}{}
	}}
	_, c1, ok1 := p.subscribe("op")
	_, c2, ok2 := p.subscribe("op")
	if !ok1 || !ok2 {
		t.Fatal("two tabs could not open a bell")
	}
	<-started
	c1()
	c1() // a second cancel is harmless
	select {
	case <-stopped:
		t.Fatal("the watcher stopped while a bell was still open")
	case <-time.After(50 * time.Millisecond):
	}
	c2()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("the watcher kept running with no bell open")
	}
	if len(started) != 0 {
		t.Error("the watcher was started more than once")
	}
}

func TestOneAccountCannotHoldEveryBell(t *testing.T) {
	p := &bellPulse{}
	for i := 0; i < bellStreamsPerUser; i++ {
		if _, _, ok := p.subscribe("op"); !ok {
			t.Fatalf("tab %d was refused under the limit", i+1)
		}
	}
	if _, _, ok := p.subscribe("op"); ok {
		t.Error("one account opened more bells than it may")
	}
	if _, _, ok := p.subscribe("someone-else"); !ok {
		t.Error("one account's tabs locked another out")
	}
}

func TestAPulseWakesEveryBellWithoutBlocking(t *testing.T) {
	p := &bellPulse{}
	a, _, _ := p.subscribe("op")
	b, _, _ := p.subscribe("other")
	p.fire()
	p.fire() // a bell already woken is not blocked on
	for _, ch := range []<-chan struct{}{a, b} {
		select {
		case <-ch:
		default:
			t.Error("a bell was not woken")
		}
	}
}

func TestEveryViewerMayOpenTheirOwnBell(t *testing.T) {
	for _, p := range []string{"/os/api/notifications/stream", "/os/api/notifications/seen", "/os/api/notifications/clear"} {
		if got := osPathMinLevel(p); got != accessAuthor {
			t.Errorf("osPathMinLevel(%s) = %d, want accessAuthor: the bell is every viewer's own", p, got)
		}
	}
}

// Audit, 3.18.12: anyone can send mail or fill in the contact form, and each
// one pulses every open bell. Unbounded, a flood of mail made each open tab
// redraw the whole bell (every mailbox counted, every hosted site checked)
// about three times a second. A bell is redrawn at most once per
// bellMinGap, however fast the pulses come.
func TestAFloodOfPulsesCannotDriveTheBellFasterThanItsGap(t *testing.T) {
	openMigratedDB(t)
	a := &App{}
	_, sig := osNotifBellSig(a.getOSSettings(context.Background()))
	events := bellStream(t, a, sig)
	time.Sleep(100 * time.Millisecond) // subscribed
	stop := time.After(bellMinGap - 200*time.Millisecond)
	for i := 0; ; i++ {
		// Each pulse brings real news, so every redraw would be sent.
		if _, err := dbpkg.DB.Exec(`INSERT INTO contact_messages(id,name,email,message,created_at) VALUES(?,?,?,?,?)`,
			"flood"+strconv.Itoa(i), "Flood "+strconv.Itoa(i), "f@example.net", "x", time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		a.bellLive.fire()
		select {
		case <-stop:
		case <-time.After(100 * time.Millisecond):
			continue
		}
		break
	}
	if n := len(events); n > 1 {
		t.Errorf("%d redraws inside one gap of %v under a flood of pulses; want at most 1", n, bellMinGap)
	}
}
