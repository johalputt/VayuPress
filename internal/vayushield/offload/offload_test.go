package offload

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func read(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, fileName))
	if os.IsNotExist(err) {
		return "" // no file == no bans
	}
	if err != nil {
		t.Fatalf("read banlist: %v", err)
	}
	return string(b)
}

func TestBanFlushWritesCanonicalEntries(t *testing.T) {
	dir := t.TempDir()
	e := New(dir)
	e.Ban("203.0.113.7", time.Hour)
	e.Ban("2001:DB8::0007", time.Hour) // non-canonical spelling
	e.Ban("not-an-ip", time.Hour)      // must be ignored
	e.Ban("198.51.100.1", 0)           // non-positive TTL ignored
	e.Flush()
	got := read(t, dir)
	if !strings.Contains(got, "203.0.113.7 ") {
		t.Fatalf("missing v4 entry:\n%s", got)
	}
	if !strings.Contains(got, "2001:db8::7 ") {
		t.Fatalf("v6 entry must be canonicalized:\n%s", got)
	}
	if strings.Contains(got, "not-an-ip") {
		t.Fatalf("invalid input leaked into the ban file:\n%s", got)
	}
	if strings.Contains(got, "198.51.100.1") {
		t.Fatalf("zero-TTL ban leaked:\n%s", got)
	}
}

func TestExpiredEntriesArePruned(t *testing.T) {
	dir := t.TempDir()
	sec := int64(1_000_000)
	e := New(dir)
	e.now = func() time.Time { return time.Unix(sec, 0) }
	e.Ban("203.0.113.8", 10*time.Second)
	e.Flush()
	if !strings.Contains(read(t, dir), "203.0.113.8") {
		t.Fatal("fresh ban should be in the file")
	}
	sec += 11
	e.Flush()
	if strings.Contains(read(t, dir), "203.0.113.8") {
		t.Fatal("expired ban must be pruned on flush")
	}
	if e.Count() != 0 {
		t.Fatalf("count = %d, want 0", e.Count())
	}
}

func TestExtendOnlyForwards(t *testing.T) {
	dir := t.TempDir()
	sec := int64(1_000_000)
	e := New(dir)
	e.now = func() time.Time { return time.Unix(sec, 0) }
	e.Ban("203.0.113.9", time.Hour)
	e.Ban("203.0.113.9", time.Minute) // shorter re-ban must not shrink expiry
	e.mu.Lock()
	exp := e.entries["203.0.113.9"]
	e.mu.Unlock()
	if exp.Unix() != sec+3600 {
		t.Fatalf("expiry = %d, want %d (longest wins)", exp.Unix(), sec+3600)
	}
}

func TestCapKeepsMostPersistentOffenders(t *testing.T) {
	dir := t.TempDir()
	e := New(dir)
	// One long-sentence offender plus a flood past the cap of short bans.
	e.Ban("203.0.113.99", 24*time.Hour)
	for i := 0; i < maxEntries+500; i++ {
		e.Ban(fmt.Sprintf("2001:db8:%x::%x", i/65000, i%65000), time.Minute)
	}
	e.Flush()
	got := read(t, dir)
	if !strings.Contains(got, "203.0.113.99") {
		t.Fatal("the longest sentence must survive the cap")
	}
	lines := strings.Count(got, "\n") - 2 // minus the two header lines
	if lines > maxEntries {
		t.Fatalf("file has %d entries, cap is %d", lines, maxEntries)
	}
}

func TestConcurrentBanFlushIsRaceFree(t *testing.T) {
	dir := t.TempDir()
	e := New(dir)
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				e.Ban(fmt.Sprintf("203.0.113.%d", (id*7+i)%250), time.Minute)
				if i%50 == 0 {
					e.Flush()
				}
			}
		}(g)
	}
	wg.Wait()
	e.Flush()
	if e.Count() == 0 {
		t.Fatal("bans should be live after concurrent writes")
	}
}

func TestProtectPreventsBan(t *testing.T) {
	dir := t.TempDir()
	e := New(dir)
	e.Protect("203.0.113.200:9999") // operator IP (with port)
	e.Ban("203.0.113.200", time.Hour)
	e.Flush()
	if strings.Contains(read(t, dir), "203.0.113.200") {
		t.Fatal("a protected operator IP must never be banned")
	}
	if e.Count() != 0 {
		t.Fatalf("count = %d, want 0", e.Count())
	}
}

func TestProtectWithdrawsExistingBan(t *testing.T) {
	dir := t.TempDir()
	e := New(dir)
	e.Ban("203.0.113.201", time.Hour)
	e.Protect("203.0.113.201")
	e.Flush()
	if strings.Contains(read(t, dir), "203.0.113.201") {
		t.Fatal("protecting an already-banned IP must withdraw the ban")
	}
}

func TestUnbanPardons(t *testing.T) {
	dir := t.TempDir()
	e := New(dir)
	e.Ban("203.0.113.202", time.Hour)
	e.Flush()
	if !strings.Contains(read(t, dir), "203.0.113.202") {
		t.Fatal("setup: ban should be present")
	}
	e.Unban("203.0.113.202:1234") // pardon (with port)
	e.Flush()
	if strings.Contains(read(t, dir), "203.0.113.202") {
		t.Fatal("unban must remove the entry so the agent lifts the kernel drop")
	}
}
