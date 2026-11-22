// SPDX-License-Identifier: Apache-2.0

package main

// vayushield_ssh.go — SSH on the host, fail2ban's job, inside Shield.
//
// The root agent reads sshd's failed sign-ins from the journal while
// ssh.want exists, and appends "<unix> <address> <user>" lines to
// ssh-failures.txt in the control directory (export_ssh_failures in
// deploy/vayushield-agent.sh). This side reads those lines, records each as
// an attempt at the SSH door on Sign-in attempts, and bans an address that
// failed sshFailures times within sshWindow through banlist.txt, which the
// agent already turns into kernel drops. The agent decides nothing; the
// exporter never bans a protected (operator) address or the machine itself.

import (
	"bufio"
	"context"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/queue"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/signin"
)

// fail2ban's defaults: five failures in ten minutes.
const (
	sshFailures = 5
	sshWindow   = 10 * time.Minute
)

// sshBanFor is how long an address is dropped for its nth ban (1-based):
// an hour, then a day, then thirty days, as a repeat offender earns.
func sshBanFor(n int) time.Duration {
	switch {
	case n <= 1:
		return time.Hour
	case n == 2:
		return 24 * time.Hour
	}
	return 30 * 24 * time.Hour
}

// sshWatch is the reader's state: where it is in the file, each address's
// recent failures and how often it was banned.
type sshWatch struct {
	mu     sync.Mutex
	offset int64
	fails  map[string][]time.Time
	bans   map[string]int
}

func newSSHWatch() *sshWatch {
	return &sshWatch{fails: map[string][]time.Time{}, bans: map[string]int{}}
}

// sshOn reports whether the operator switched SSH protection on.
func (a *App) sshOn(ctx context.Context) bool {
	return a.siteSettings != nil && a.siteSettings.Get(ctx, settings.ForPrimary(), settings.KeyShieldSSH) == "on"
}

// applySSHSetting asks the agent to read sshd's journal, or to stop.
func (a *App) applySSHSetting(ctx context.Context) {
	want := filepath.Join(shieldControlDir(), "ssh.want")
	if a.sshOn(ctx) {
		if f, err := os.OpenFile(want, os.O_CREATE|os.O_WRONLY, 0o640); err == nil {
			_ = f.Close()
		}
		return
	}
	_ = os.Remove(want)
}

// shieldSSHState is what the agent last wrote about reading sshd's journal.
func shieldSSHState() string {
	b, err := os.ReadFile(filepath.Join(shieldControlDir(), "ssh.state"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// watchSSH reads the agent's failures every ten seconds.
func (a *App) watchSSH() {
	a.applySSHSetting(context.Background())
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-queue.DoneCh:
				return
			case <-t.C:
				a.readSSHFailures(time.Now())
			}
		}
	}()
}

// readSSHFailures takes the lines added since the last read. A file the
// agent emptied (it is capped) is read again from its start.
func (a *App) readSSHFailures(now time.Time) {
	if a.sshWatch == nil || !a.sshOn(context.Background()) {
		return
	}
	f, err := os.Open(filepath.Join(shieldControlDir(), "ssh-failures.txt")) //nolint:gosec // a fixed name in the control directory
	if err != nil {
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return
	}
	w := a.sshWatch
	w.mu.Lock()
	defer w.mu.Unlock()
	if st.Size() < w.offset {
		w.offset = 0
	}
	if _, err := f.Seek(w.offset, io.SeekStart); err != nil {
		return
	}
	r := bufio.NewReader(io.LimitReader(f, 4<<20))
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			break // a line not yet ended is read next time
		}
		w.offset += int64(len(line))
		a.noteSSHFailureLocked(strings.TrimSpace(line), now)
	}
}

// noteSSHFailureLocked counts one "<unix> <address> <user>" line, and bans
// its address at the threshold. Caller holds a.sshWatch.mu.
func (a *App) noteSSHFailureLocked(line string, now time.Time) {
	f := strings.Fields(line)
	if len(f) != 3 {
		return
	}
	sec, err := strconv.ParseInt(f[0], 10, 64)
	if err != nil {
		return
	}
	at := time.Unix(sec, 0)
	addr, err := netip.ParseAddr(f[1])
	if err != nil || now.Sub(at) > sshWindow {
		return
	}
	ip := addr.Unmap().String()
	a.signinRecord(f[2], signin.DoorSSH, signin.StagePassword, ip)
	w := a.sshWatch
	kept := w.fails[ip][:0]
	for _, t := range w.fails[ip] {
		if now.Sub(t) < sshWindow {
			kept = append(kept, t)
		}
	}
	kept = append(kept, at)
	if len(kept) < sshFailures {
		w.fails[ip] = kept
		return
	}
	delete(w.fails, ip)
	w.bans[ip]++
	ttl := sshBanFor(w.bans[ip])
	if a.shieldOffload != nil {
		a.shieldOffload.Ban(ip, ttl)
	}
	logging.LogWarn("vayushield", "SSH: "+ip+" failed "+strconv.Itoa(sshFailures)+" sign-ins within "+sshWindow.String()+"; dropped for "+ttl.String())
}
