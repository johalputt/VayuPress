// SPDX-License-Identifier: Apache-2.0

// Package community is the attacker list VayuPress installs share: each
// install that opts in reports the addresses it banned for attacking it, a
// hub (an install the operator chose, johal.in first) lists an address once
// enough independent installs reported it, and every install that opts in
// reads the hub's list through intel, as the abusive kind (it refuses a
// sign-in and weighs a score; it never refuses a page).
//
// The hub is public, so the rules are what hold it against poisoning:
//
//   - every report is signed by its install's own Ed25519 key; the key is
//     the reporter, and the hub can remove one, with all it reported;
//   - one report per key per ReportEvery, and at most MaxAddrs addresses in
//     it; at most NewKeysPerNet new keys from one network a day;
//   - an address is listed only when reporters on ListAt different networks
//     (a /24, or a /48 for IPv6, of where the report came from, as the hub
//     saw it, not as the report claims) reported it within ListWindow;
//   - a listing ends ListFor after the last report that kept it at ListAt;
//   - only public unicast addresses are accepted, never a range;
//   - the served list is signed by the hub's key, which an install pins on
//     its first read.
//
// Addresses only: no account, no path, no time of day beyond the report's.
package community

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	MaxAddrs    = 500
	MaxBody     = 64 << 10
	ReportEvery = 5 * time.Minute
	ListAt      = 3
	ListWindow  = 24 * time.Hour
	ListFor     = 7 * 24 * time.Hour
	// NewKeysPerNet is how many new reporters one network may register a day.
	NewKeysPerNet = 4
	// clockSkew is how far a report's time may be from the hub's: a replay of
	// an old report is refused, and so is one dated ahead.
	clockSkew = 15 * time.Minute
	listMagic = "vayushield-community 1"
)

// Report is what an install sends: its public key, when, and the addresses.
type Report struct {
	Key   string   `json:"key"` // base64 Ed25519 public key
	At    int64    `json:"at"`  // unix seconds
	Addrs []string `json:"addrs"`
}

// SignReport is the body to send and its signature (base64), for the
// X-VayuShield-Signature header.
func SignReport(priv ed25519.PrivateKey, at time.Time, addrs []string) (body []byte, sig string) {
	pub, _ := priv.Public().(ed25519.PublicKey)
	body, _ = json.Marshal(Report{Key: base64.StdEncoding.EncodeToString(pub), At: at.Unix(), Addrs: addrs})
	return body, base64.StdEncoding.EncodeToString(ed25519.Sign(priv, body))
}

// Errors a hub answers with.
var (
	ErrSignature = errors.New("the report's signature does not verify")
	ErrClock     = errors.New("the report's time is too far from the hub's")
	ErrTooMany   = errors.New("the report carries too many addresses")
	ErrRemoved   = errors.New("this reporter was removed by the hub's operator")
	ErrTooSoon   = errors.New("this reporter reported too recently")
)

// VerifyReport checks a report's signature against the key it carries and
// its time against now, and keeps only the public unicast addresses in it.
func VerifyReport(body []byte, sig string, now time.Time) (Report, []netip.Addr, error) {
	var rep Report
	if len(body) > MaxBody || json.Unmarshal(body, &rep) != nil {
		return rep, nil, ErrSignature
	}
	pub, err := base64.StdEncoding.DecodeString(rep.Key)
	s, err2 := base64.StdEncoding.DecodeString(sig)
	if err != nil || err2 != nil || len(pub) != ed25519.PublicKeySize || !ed25519.Verify(pub, body, s) {
		return rep, nil, ErrSignature
	}
	if d := now.Sub(time.Unix(rep.At, 0)); d > clockSkew || d < -clockSkew {
		return rep, nil, ErrClock
	}
	if len(rep.Addrs) > MaxAddrs {
		return rep, nil, ErrTooMany
	}
	var out []netip.Addr
	seen := map[netip.Addr]bool{}
	for _, s := range rep.Addrs {
		a, err := netip.ParseAddr(strings.TrimSpace(s))
		if err != nil {
			continue
		}
		a = a.Unmap()
		if Public(a) && !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	return rep, out, nil
}

// Public is a routable unicast address: never a private, loopback,
// link-local, multicast, unspecified or documentation address.
func Public(a netip.Addr) bool {
	if !a.IsValid() || a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() ||
		a.IsMulticast() || a.IsUnspecified() || a.IsInterfaceLocalMulticast() {
		return false
	}
	for _, p := range nonPublic {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

var nonPublic = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"), // carrier-grade NAT
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"), // documentation
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"), // documentation
	netip.MustParsePrefix("203.0.113.0/24"),  // documentation
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:db8::/32"), // documentation
}

// Network is the reporter's network for counting independence: its /24, or
// its /48 for IPv6.
func Network(a netip.Addr) string {
	a = a.Unmap()
	bits := 24
	if a.Is6() {
		bits = 48
	}
	p, err := a.Prefix(bits)
	if err != nil {
		return ""
	}
	return p.String()
}

// SignList is the served list: a first line naming the format and when, an
// address a line, and a last line with the hub's signature over everything
// before it.
func SignList(priv ed25519.PrivateKey, at time.Time, addrs []string) []byte {
	var b bytes.Buffer
	b.WriteString(listMagic + " " + strconv.FormatInt(at.Unix(), 10) + "\n")
	for _, a := range addrs {
		b.WriteString(a + "\n")
	}
	sig := ed25519.Sign(priv, b.Bytes())
	b.WriteString("sig " + base64.StdEncoding.EncodeToString(sig) + "\n")
	return b.Bytes()
}

// VerifyList checks a served list against the pinned hub key and returns its
// addresses as single-address prefixes.
func VerifyList(body []byte, pub ed25519.PublicKey) ([]netip.Prefix, error) {
	if len(pub) != ed25519.PublicKeySize {
		return nil, errors.New("no hub key is pinned")
	}
	i := bytes.LastIndex(bytes.TrimRight(body, "\n"), []byte("\nsig "))
	if i < 0 || !bytes.HasPrefix(body, []byte(listMagic+" ")) {
		return nil, errors.New("not a shared attacker list")
	}
	signed, tail := body[:i+1], strings.TrimSpace(string(body[i+1:]))
	sig, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(tail, "sig "))
	if err != nil || !ed25519.Verify(pub, signed, sig) {
		return nil, errors.New("the list's signature does not verify against the pinned hub key")
	}
	var out []netip.Prefix
	sc := bufio.NewScanner(bytes.NewReader(signed))
	sc.Scan() // the first line
	for sc.Scan() {
		a, err := netip.ParseAddr(strings.TrimSpace(sc.Text()))
		if err != nil || !Public(a.Unmap()) {
			continue
		}
		a = a.Unmap()
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}

// Hub is the hub's store (migration 136).
type Hub struct {
	DB  *sql.DB
	Now func() time.Time
}

func (h *Hub) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

// Accept records a verified report from the address it was posted from, and
// lists each address that now has ListAt networks within ListWindow.
func (h *Hub) Accept(ctx context.Context, rep Report, addrs []netip.Addr, from netip.Addr) error {
	now := h.now()
	net := Network(from)
	if net == "" {
		return errors.New("the report came from no address")
	}
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var removed int
	var last int64
	err = tx.QueryRowContext(ctx, `SELECT removed, last_report FROM community_reporters WHERE key = ?`, rep.Key).Scan(&removed, &last)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// Keys cost nothing to make; networks do. A network that registered
		// NewKeysPerNet keys within a day registers no more until it passes.
		var fresh int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM community_reporters WHERE net = ? AND first_seen > ?`, net, now.Add(-24*time.Hour).Unix()).Scan(&fresh); err != nil {
			return err
		}
		if fresh >= NewKeysPerNet {
			return ErrTooSoon
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO community_reporters(key, net, first_seen) VALUES(?, ?, ?)`, rep.Key, net, now.Unix()); err != nil {
			return err
		}
	case err != nil:
		return err
	case removed != 0:
		return ErrRemoved
	case now.Sub(time.Unix(last, 0)) < ReportEvery:
		return ErrTooSoon
	}
	if _, err := tx.ExecContext(ctx, `UPDATE community_reporters SET net = ?, last_report = ?, reports = reports + 1 WHERE key = ?`, net, now.Unix(), rep.Key); err != nil {
		return err
	}
	since := now.Add(-ListWindow).Unix()
	for _, a := range addrs {
		s := a.String()
		if _, err := tx.ExecContext(ctx, `INSERT INTO community_reports(addr, key, net, at) VALUES(?, ?, ?, ?) ON CONFLICT(addr, key) DO UPDATE SET net = excluded.net, at = excluded.at`, s, rep.Key, net, now.Unix()); err != nil {
			return err
		}
		var nets int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(DISTINCT net) FROM community_reports WHERE addr = ? AND at >= ?`, s, since).Scan(&nets); err != nil {
			return err
		}
		if nets >= ListAt {
			if _, err := tx.ExecContext(ctx, `INSERT INTO community_listed(addr, until) VALUES(?, ?) ON CONFLICT(addr) DO UPDATE SET until = excluded.until`, s, now.Add(ListFor).Unix()); err != nil {
				return err
			}
		}
	}
	// What has run out goes: reports past the longest a listing lasts, and
	// listings past their end.
	if _, err := tx.ExecContext(ctx, `DELETE FROM community_reports WHERE at < ?`, now.Add(-ListFor).Unix()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM community_listed WHERE until <= ?`, now.Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

// Listed is every address listed now, sorted.
func (h *Hub) Listed(ctx context.Context) ([]string, error) {
	rows, err := h.DB.QueryContext(ctx, `SELECT addr FROM community_listed WHERE until > ? ORDER BY addr`, h.now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Reporter is one install that reported, for the hub's page.
type Reporter struct {
	Key, Net         string
	First, Last      time.Time
	Reports, Current int // reports sent; addresses it reported still held
	Removed          bool
}

// Reporters lists every reporter, the latest first.
func (h *Hub) Reporters(ctx context.Context) ([]Reporter, error) {
	rows, err := h.DB.QueryContext(ctx, `SELECT r.key, r.net, r.first_seen, r.last_report, r.reports, r.removed, (SELECT COUNT(1) FROM community_reports p WHERE p.key = r.key) FROM community_reporters r`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Reporter
	for rows.Next() {
		var r Reporter
		var first, last int64
		var removed int
		if err := rows.Scan(&r.Key, &r.Net, &first, &last, &r.Reports, &removed, &r.Current); err != nil {
			return nil, err
		}
		r.First, r.Last, r.Removed = time.Unix(first, 0), time.Unix(last, 0), removed != 0
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Last.After(out[j].Last) })
	return out, nil
}

// Remove refuses a reporter from now on, deletes what it reported, and ends
// each listing that no longer has ListAt networks without it.
func (h *Hub) Remove(ctx context.Context, key string) error {
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `UPDATE community_reporters SET removed = 1 WHERE key = ?`, key)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no reporter with that key")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM community_reports WHERE key = ?`, key); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM community_listed WHERE (SELECT COUNT(DISTINCT net) FROM community_reports p WHERE p.addr = community_listed.addr) < ?`, ListAt); err != nil {
		return err
	}
	return tx.Commit()
}
