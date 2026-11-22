// SPDX-License-Identifier: Apache-2.0

package community

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"database/sql"
	"errors"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

const attacker = "185.220.101.7"

func hub(t *testing.T) (*Hub, *time.Time) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	up, err := os.ReadFile("../../db/migrations/136-shield-community.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(up), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "--") {
			if _, err := db.Exec(line); err != nil {
				t.Fatalf("%s: %v", line, err)
			}
		}
	}
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	return &Hub{DB: db, Now: func() time.Time { return now }}, &now
}

func key(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return priv
}

// report signs, verifies and accepts one report of addrs from an install
// posting from `from`.
func report(t *testing.T, h *Hub, priv ed25519.PrivateKey, from string, addrs ...string) error {
	t.Helper()
	body, sig := SignReport(priv, h.Now(), addrs)
	rep, ok, err := VerifyReport(body, sig, h.Now())
	if err != nil {
		t.Fatalf("a signed report did not verify: %v", err)
	}
	return h.Accept(context.Background(), rep, ok, netip.MustParseAddr(from))
}

func listed(t *testing.T, h *Hub, addr string) bool {
	t.Helper()
	l, err := h.Listed(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range l {
		if a == addr {
			return true
		}
	}
	return false
}

// A report verifies only against the key it carries, unaltered.
func TestAReportVerifiesOnlyAsSigned(t *testing.T) {
	now := time.Now()
	priv := key(t)
	body, sig := SignReport(priv, now, []string{attacker})
	if _, _, err := VerifyReport(body, sig, now); err != nil {
		t.Fatalf("a signed report: %v", err)
	}
	altered := bytes.Replace(body, []byte(attacker), []byte("185.220.101.8"), 1)
	if _, _, err := VerifyReport(altered, sig, now); !errors.Is(err, ErrSignature) {
		t.Fatalf("an altered report: %v", err)
	}
	_, other := SignReport(key(t), now, []string{attacker})
	if _, _, err := VerifyReport(body, other, now); !errors.Is(err, ErrSignature) {
		t.Fatalf("another key's signature: %v", err)
	}
}

// A report dated more than the skew from the hub's clock is refused, so a
// captured one cannot be sent again later.
func TestAnOldReportIsRefused(t *testing.T) {
	now := time.Now()
	body, sig := SignReport(key(t), now.Add(-20*time.Minute), []string{attacker})
	if _, _, err := VerifyReport(body, sig, now); !errors.Is(err, ErrClock) {
		t.Fatalf("a report twenty minutes old: %v", err)
	}
}

// Only public addresses are kept: a private, loopback or documentation
// address in a report is dropped, and so is a range.
func TestOnlyPublicAddressesAreKept(t *testing.T) {
	now := time.Now()
	body, sig := SignReport(key(t), now, []string{"10.0.0.1", "127.0.0.1", "192.0.2.4", "100.64.1.1", "185.0.0.0/8", attacker, attacker})
	_, addrs, err := VerifyReport(body, sig, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(addrs) != 1 || addrs[0].String() != attacker {
		t.Fatalf("kept %v", addrs)
	}
}

// Two networks do not list an address; a third does. Three keys on one
// network are one network.
func TestThreeNetworksListAnAddressAndOneNetworkIsOne(t *testing.T) {
	h, _ := hub(t)
	for _, from := range []string{"9.9.1.1", "9.9.1.2", "9.9.1.3"} {
		if err := report(t, h, key(t), from, attacker); err != nil {
			t.Fatal(err)
		}
	}
	if listed(t, h, attacker) {
		t.Fatal("three keys on one network listed an address")
	}
	if err := report(t, h, key(t), "8.8.2.2", attacker); err != nil {
		t.Fatal(err)
	}
	if listed(t, h, attacker) {
		t.Fatal("two networks listed an address")
	}
	if err := report(t, h, key(t), "7.7.3.3", attacker); err != nil {
		t.Fatal(err)
	}
	if !listed(t, h, attacker) {
		t.Fatal("three networks did not list an address")
	}
}

// Three networks over more than a day do not list it.
func TestReportsMustFallWithinADay(t *testing.T) {
	h, now := hub(t)
	_ = report(t, h, key(t), "9.9.1.1", attacker)
	*now = now.Add(2 * time.Hour)
	_ = report(t, h, key(t), "8.8.2.2", attacker)
	*now = now.Add(23 * time.Hour)
	_ = report(t, h, key(t), "7.7.3.3", attacker)
	if listed(t, h, attacker) {
		t.Fatal("reports twenty-five hours apart listed an address")
	}
}

// A listing ends a week after the report that kept it.
func TestAListingEndsAfterAWeek(t *testing.T) {
	h, now := hub(t)
	for _, from := range []string{"9.9.1.1", "8.8.2.2", "7.7.3.3"} {
		_ = report(t, h, key(t), from, attacker)
	}
	*now = now.Add(ListFor - time.Minute)
	if !listed(t, h, attacker) {
		t.Fatal("a listing ended before its week")
	}
	*now = now.Add(2 * time.Minute)
	if listed(t, h, attacker) {
		t.Fatal("a listing outlived its week")
	}
}

// One key reports at most once in ReportEvery.
func TestAReporterWaitsBetweenReports(t *testing.T) {
	h, now := hub(t)
	priv := key(t)
	if err := report(t, h, priv, "9.9.1.1", attacker); err != nil {
		t.Fatal(err)
	}
	if err := report(t, h, priv, "9.9.1.1", attacker); !errors.Is(err, ErrTooSoon) {
		t.Fatalf("a second report at once: %v", err)
	}
	*now = now.Add(ReportEvery)
	if err := report(t, h, priv, "9.9.1.1", attacker); err != nil {
		t.Fatalf("a report after the wait: %v", err)
	}
}

// One network registers at most NewKeysPerNet keys a day.
func TestANetworkRegistersFewKeysADay(t *testing.T) {
	h, now := hub(t)
	for i := 0; i < NewKeysPerNet; i++ {
		if err := report(t, h, key(t), "9.9.1.1", attacker); err != nil {
			t.Fatal(err)
		}
	}
	if err := report(t, h, key(t), "9.9.1.1", attacker); !errors.Is(err, ErrTooSoon) {
		t.Fatalf("one key past the day's allowance: %v", err)
	}
	*now = now.Add(25 * time.Hour)
	if err := report(t, h, key(t), "9.9.1.1", attacker); err != nil {
		t.Fatalf("a key the next day: %v", err)
	}
}

// A removed reporter's reports go, the listings that leaned on them end, and
// it is refused from then on.
func TestARemovedReporterTakesItsReportsWithIt(t *testing.T) {
	h, now := hub(t)
	keys := []ed25519.PrivateKey{key(t), key(t), key(t)}
	for i, from := range []string{"9.9.1.1", "8.8.2.2", "7.7.3.3"} {
		_ = report(t, h, keys[i], from, attacker)
	}
	if !listed(t, h, attacker) {
		t.Fatal("not listed to begin with")
	}
	rs, err := h.Reporters(context.Background())
	if err != nil || len(rs) != 3 {
		t.Fatalf("reporters: %v %v", rs, err)
	}
	var k string
	for _, r := range rs {
		if r.Net == "7.7.3.0/24" {
			k = r.Key
		}
	}
	if k == "" {
		t.Fatal("no reporter on 7.7.3.0/24")
	}
	if err := h.Remove(context.Background(), k); err != nil {
		t.Fatal(err)
	}
	if listed(t, h, attacker) {
		t.Fatal("a listing that leaned on a removed reporter stayed")
	}
	*now = now.Add(ReportEvery)
	if err := report(t, h, keys[2], "7.7.3.3", attacker); !errors.Is(err, ErrRemoved) {
		t.Fatalf("a removed reporter reported: %v", err)
	}
}

// A served list reads only against the hub's key, unaltered.
func TestAListReadsOnlyAgainstTheHubKey(t *testing.T) {
	priv := key(t)
	body := SignList(priv, time.Now(), []string{attacker, "2a0b:f4c0::1"})
	got, err := VerifyList(body, priv.Public().(ed25519.PublicKey))
	if err != nil || len(got) != 2 || got[0].String() != attacker+"/32" || got[1].String() != "2a0b:f4c0::1/128" {
		t.Fatalf("read %v, %v", got, err)
	}
	added := bytes.Replace(body, []byte(attacker+"\n"), []byte(attacker+"\n185.220.101.9\n"), 1)
	if _, err := VerifyList(added, priv.Public().(ed25519.PublicKey)); err == nil {
		t.Fatal("a list with an address added was read")
	}
	if _, err := VerifyList(body, key(t).Public().(ed25519.PublicKey)); err == nil {
		t.Fatal("a list was read against another key")
	}
}
