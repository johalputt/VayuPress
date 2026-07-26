// SPDX-License-Identifier: Apache-2.0

package vayutor

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// fakeTorServer is an in-process TCP control port that mints a distinct onion
// per ADD_ONION so the engine's registry can be checked end-to-end.
func fakeTorServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var n int64
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				r := bufio.NewReader(c)
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					line = strings.TrimRight(line, "\r\n")
					switch {
					case strings.HasPrefix(line, "AUTHENTICATE"):
						c.Write([]byte("250 OK\r\n"))
					case strings.HasPrefix(line, "ADD_ONION NEW:"):
						id := fmt.Sprintf("onion%016d", atomic.AddInt64(&n, 1))
						c.Write([]byte("250-ServiceID=" + id + "\r\n250-PrivateKey=ED25519-V3:KEY" + id + "\r\n250 OK\r\n"))
					case strings.HasPrefix(line, "ADD_ONION ED25519-V3:"):
						// Re-attach: derive the id back from the stored key blob.
						key := strings.TrimPrefix(line, "ADD_ONION ED25519-V3:")
						key = strings.SplitN(key, " ", 2)[0]
						id := strings.TrimPrefix(key, "KEY")
						c.Write([]byte("250-ServiceID=" + id + "\r\n250 OK\r\n"))
					case strings.HasPrefix(line, "DEL_ONION"):
						c.Write([]byte("250 OK\r\n"))
					default:
						c.Write([]byte("510 Unrecognized command\r\n"))
					}
				}
			}(conn)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return ln.Addr().String()
}

// memStore is an in-memory Store for tests.
type memStore struct {
	mu       sync.Mutex
	onions   map[string]OnionRecord
	visits   int64
	pageHits map[string]int64
}

func newMemStore() *memStore {
	return &memStore{onions: map[string]OnionRecord{}, pageHits: map[string]int64{}}
}

func (m *memStore) LoadOnions(context.Context) ([]OnionRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []OnionRecord
	for _, r := range m.onions {
		out = append(out, r)
	}
	return out, nil
}
func (m *memStore) SaveOnion(_ context.Context, rec OnionRecord) error {
	m.mu.Lock()
	m.onions[rec.Host] = rec
	m.mu.Unlock()
	return nil
}
func (m *memStore) DeleteOnion(_ context.Context, host string) error {
	m.mu.Lock()
	delete(m.onions, host)
	m.mu.Unlock()
	return nil
}
func (m *memStore) LoadVisits(context.Context) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.visits
}
func (m *memStore) SaveVisits(_ context.Context, n int64) error {
	m.mu.Lock()
	m.visits = n
	m.mu.Unlock()
	return nil
}
func (m *memStore) LoadPageHits(context.Context) map[string]int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]int64, len(m.pageHits))
	for k, v := range m.pageHits {
		out[k] = v
	}
	return out
}
func (m *memStore) SavePageHits(_ context.Context, hits map[string]int64) error {
	m.mu.Lock()
	m.pageHits = hits
	m.mu.Unlock()
	return nil
}

func newTestEngine(t *testing.T, active *bool, hosts []string) (*Engine, *memStore) {
	addr := fakeTorServer(t)
	store := newMemStore()
	e := NewEngine(Config{
		Enabled:     true,
		ControlAddr: addr,
		Target:      "127.0.0.1:8080",
		Store:       store,
		Domains:     func(context.Context) ([]string, error) { return hosts, nil },
		Active:      func() bool { return *active },
	})
	return e, store
}

func TestReconcileCreatesOnionPerDomain(t *testing.T) {
	on := true
	e, store := newTestEngine(t, &on, []string{"a.example", "b.example"})
	e.reconcile(context.Background())

	for _, h := range []string{"a.example", "b.example"} {
		onion := e.OnionForHost(h)
		if onion == "" || !strings.HasSuffix(onion, ".onion") {
			t.Fatalf("no onion for %s: %q", h, onion)
		}
		if back, ok := e.HostForOnion(onion); !ok || back != h {
			t.Errorf("reverse map %s -> %q (ok=%v), want %s", onion, back, ok, h)
		}
	}
	// Two distinct onions.
	if e.OnionForHost("a.example") == e.OnionForHost("b.example") {
		t.Error("each domain must get a distinct onion")
	}
	// Identities persisted for a stable address next boot.
	if recs, _ := store.LoadOnions(context.Background()); len(recs) != 2 {
		t.Errorf("persisted %d onions, want 2", len(recs))
	}
}

func TestReconcileTogglesOff(t *testing.T) {
	on := true
	e, _ := newTestEngine(t, &on, []string{"a.example"})
	e.reconcile(context.Background())
	if e.OnionForHost("a.example") == "" {
		t.Fatal("expected an onion while active")
	}
	on = false
	e.reconcile(context.Background())
	if e.OnionForHost("a.example") != "" {
		t.Error("toggling off must remove the live mapping")
	}
	if st := e.Snapshot(); st.Active {
		t.Error("snapshot should report inactive")
	}
}

func TestReconcileDropsRemovedDomain(t *testing.T) {
	on := true
	addr := fakeTorServer(t)
	store := newMemStore()
	hosts := []string{"a.example", "b.example"}
	e := NewEngine(Config{
		Enabled: true, ControlAddr: addr, Target: "127.0.0.1:8080", Store: store,
		Domains: func(context.Context) ([]string, error) { return hosts, nil },
		Active:  func() bool { return on },
	})
	e.reconcile(context.Background())
	hosts = []string{"a.example"} // b removed
	e.reconcile(context.Background())
	if e.OnionForHost("b.example") != "" {
		t.Error("removed domain must lose its onion mapping")
	}
	if recs, _ := store.LoadOnions(context.Background()); len(recs) != 1 {
		t.Errorf("stored identities = %d, want 1 after removal", len(recs))
	}
}

func TestStableAddressAcrossReconcile(t *testing.T) {
	on := true
	e, _ := newTestEngine(t, &on, []string{"a.example"})
	e.reconcile(context.Background())
	first := e.OnionForHost("a.example")
	e.reconcile(context.Background()) // re-attach using the persisted key
	if second := e.OnionForHost("a.example"); second != first {
		t.Errorf("onion address changed across reconcile: %q -> %q", first, second)
	}
}

func TestVisitCounter(t *testing.T) {
	e := NewEngine(Config{Enabled: true})
	if e.Visits() != 0 {
		t.Fatal("fresh engine should have 0 visits")
	}
	for i := 0; i < 5; i++ {
		e.IncVisit()
	}
	if e.Visits() != 5 {
		t.Errorf("visits = %d, want 5", e.Visits())
	}
}

func TestNormHost(t *testing.T) {
	cases := map[string]string{
		"Example.COM":     "example.com",
		"example.com:443": "example.com",
		"example.com.":    "example.com",
		"  Foo.io  ":      "foo.io",
	}
	for in, want := range cases {
		if got := normHost(in); got != want {
			t.Errorf("normHost(%q) = %q, want %q", in, got, want)
		}
	}
}
