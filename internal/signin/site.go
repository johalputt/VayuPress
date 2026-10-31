// SPDX-License-Identifier: Apache-2.0

package signin

import (
	"strconv"
	"strings"
	"time"
)

// siteEntry is a whole-site block as the request path reads it.
type siteEntry struct {
	id    int64
	until time.Time // zero: always
}

// rebuildSiteLocked takes a fresh copy of the whole-site blocks for the
// request path, which reads it without the lock. Caller holds s.mu.
func (s *Store) rebuildSiteLocked() {
	m := map[string]siteEntry{}
	for _, b := range s.blocks {
		if b.Scope != "site" {
			continue
		}
		prefix := "a:"
		if b.Kind == "network" {
			prefix = "n:"
		}
		m[prefix+b.Key] = siteEntry{id: b.ID, until: b.Until}
	}
	s.site.Store(&m)
}

// RefuseRequest reports whether a request from addr is refused by a
// whole-site block of its address or its network. With no such block it
// costs a pointer read: no fingerprint is made.
func (s *Store) RefuseRequest(addr string) bool {
	if s == nil {
		return false
	}
	snap := s.site.Load()
	if snap == nil || len(*snap) == 0 {
		return false
	}
	src, ok := s.source(addr, false)
	if !ok {
		return false
	}
	now := s.Now()
	for _, k := range []string{"a:" + src.fp, "n:" + src.net} {
		if e, ok := (*snap)[k]; ok && (e.until.IsZero() || now.Before(e.until)) {
			s.mu.Lock()
			s.hits[e.id]++
			s.mu.Unlock()
			return true
		}
	}
	return false
}

// maskedPrivate reports whether a masked address is on a private, loopback
// or link-local network, read from the part the mask keeps.
func maskedPrivate(masked string) bool {
	if a, b, ok := strings.Cut(masked, "."); ok {
		first, _ := strconv.Atoi(a)
		second, _ := strconv.Atoi(strings.SplitN(b, ".", 2)[0])
		return first == 10 || first == 127 || (first == 192 && second == 168) ||
			(first == 172 && second >= 16 && second <= 31) || (first == 169 && second == 254) || first == 0
	}
	g := strings.ToLower(strings.SplitN(masked, ":", 2)[0])
	return strings.HasPrefix(g, "fc") || strings.HasPrefix(g, "fd") || strings.HasPrefix(g, "fe8") ||
		strings.HasPrefix(g, "fe9") || strings.HasPrefix(g, "fea") || strings.HasPrefix(g, "feb") || g == "0"
}
