// SPDX-License-Identifier: Apache-2.0

// Package signin keeps the record of failed sign-ins at every door, and the
// blocks that stop the sources behind them.
//
// An address is never kept whole. Each attempt is stored with the address's
// last part hidden (103.21.x.x) and with keyed fingerprints of the address and
// of its network, made with a key held outside the database (secrets.Derive).
// A block is a fingerprint, so it holds exactly, while neither a reader of the
// page nor a reader of the database file can get the address back: without the
// key, the four billion IPv4 addresses cannot be tried against it.
//
// Attempts are counted, not logged one by one: a minute's failures by one
// source, on one account, at one door and stage, are one row with a count. A
// flood adds to counts, and the counts waiting for the next flush are capped.
package signin

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The doors a sign-in comes through.
const (
	DoorWeb      = "web"      // the site's mail sign-in and the app's
	DoorConsole  = "console"  // /os/login
	DoorIMAP     = "imap"     //
	DoorPOP3     = "pop3"     //
	DoorSMTP     = "smtp"     // submission
	DoorDAV      = "dav"      // contacts and calendars
	DoorTalk     = "talk"     // VayuTalk's connect
	DoorRecovery = "recovery" // the recovery code
)

// The stages an attempt reached.
const (
	// StagePassword: the password was wrong.
	StagePassword = "password"
	// StageCode: the password was right and the second factor wrong. Whoever
	// it was knows the password; the holder is told at once.
	StageCode = "code"
	// StagePaused: refused unchecked, the account being paused (auth's
	// account lockout).
	StagePaused = "paused"
	// StageOK: signed in. Kept for the interactive doors only, so the holder
	// sees their own sign-ins among the failures; a mail app signing in every
	// few minutes is not recorded.
	StageOK = "ok"
)

// The automatic block of password spraying: a source failing on SprayAccounts
// or more accounts within SprayWindow is refused sign-in for SprayFirst. One
// caught again within SprayRepeat of its last block ending is refused for
// twice that block, up to SprayMax.
const (
	SprayAccounts = 3
	SprayWindow   = time.Hour
	SprayFirst    = 6 * time.Hour
	SprayMax      = 24 * time.Hour
	SprayRepeat   = 24 * time.Hour
)

// Retention is how long attempts are kept.
const Retention = 30 * 24 * time.Hour

const (
	// pendingCap bounds the counts held between flushes. Past it, an attempt
	// opening a new count is dropped and counted as dropped; one adding to a
	// count is kept.
	pendingCap = 5000
	// sprayCap bounds the sources followed for spraying.
	sprayCap = 20000
	// alarmEvery is how often one source's right-password alarm is repeated
	// for one account.
	alarmEvery = time.Hour
)

// ErrSelf refuses a block that would stop the operator making it.
var ErrSelf = errors.New("signin: this block would stop your own sign-in")

// Attempt is one sign-in, as a door saw it.
type Attempt struct {
	Account string // the address or name given, as auth.AccountKey keys it
	Door    string
	Stage   string
	IP      string // the source address, with or without its port
}

// Alarm is a right password with a wrong second factor.
type Alarm struct {
	Account string
	Door    string
	Masked  string
	Country string
	At      time.Time
}

// Source is what is known of one address.
type Source struct {
	FP       string
	Masked   string
	Country  string
	Failures int
	Accounts int
	Doors    []string
	Last     time.Time
	Known    bool // the password was right at least once (a code stage)
	Mine     bool // a holder said it was them
	Block    *Block
}

// Block refuses sign-in to a source, a network or a country.
type Block struct {
	ID      int64
	Kind    string // address, network, country
	Key     string // a fingerprint, or an ISO country code
	Label   string // what the page shows: 103.21.x.x, 103.21.x.0/24, VN
	Account string // "" for the whole install
	Reason  string // "spraying", or who set it
	Created time.Time
	Until   time.Time // zero: always
	Hits    int
}

// Active reports whether b refuses at now.
func (b *Block) Active(now time.Time) bool {
	return b.Until.IsZero() || now.Before(b.Until)
}

// Refusal is why a sign-in was refused before its password was checked.
type Refusal struct {
	Block *Block // nil when the account's fence refused it
	Fence bool
}

type pendKey struct {
	minute                   int64
	account, door, stage, fp string
}

type pendVal struct {
	net, masked, country string
	n                    int
}

type sprayState struct {
	accounts map[string]time.Time
}

type alarmKey struct{ account, fp string }

// Store is the record and its blocks.
type Store struct {
	db  *sql.DB
	key []byte

	// Now is the clock, for tests.
	Now func() time.Time
	// OnAlarm is called, off the caller's goroutine, on a right password with
	// a wrong second factor.
	OnAlarm func(Alarm)

	mu       sync.Mutex
	pending  map[pendKey]*pendVal
	dropped  int
	blocks   []*Block
	hits     map[int64]int
	fences   map[string]map[string]bool
	known    map[string]map[string]bool // account → fingerprints
	spray    map[string]*sprayState
	alarmed  map[alarmKey]time.Time
	lastTrim time.Time
}

// Open loads the blocks, fences and known places. key is the fingerprints'
// key; it must not be stored in db.
func Open(ctx context.Context, db *sql.DB, key []byte) (*Store, error) {
	if len(key) < 16 {
		return nil, errors.New("signin: fingerprint key too short")
	}
	s := &Store{
		db: db, key: append([]byte(nil), key...), Now: time.Now,
		pending: map[pendKey]*pendVal{}, hits: map[int64]int{},
		fences: map[string]map[string]bool{}, known: map[string]map[string]bool{},
		spray: map[string]*sprayState{}, alarmed: map[alarmKey]time.Time{},
	}
	if err := s.reload(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) reload(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id, kind, key, label, account, reason, created, until, hits FROM signin_blocks WHERE lifted = 0`)
	if err != nil {
		return err
	}
	var blocks []*Block
	for rows.Next() {
		var b Block
		var created, until int64
		if err := rows.Scan(&b.ID, &b.Kind, &b.Key, &b.Label, &b.Account, &b.Reason, &created, &until, &b.Hits); err != nil {
			rows.Close()
			return err
		}
		b.Created = time.Unix(created, 0)
		if until > 0 {
			b.Until = time.Unix(until, 0)
		}
		blocks = append(blocks, &b)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	fences := map[string]map[string]bool{}
	frows, err := s.db.QueryContext(ctx, `SELECT account, countries FROM signin_fences`)
	if err != nil {
		return err
	}
	for frows.Next() {
		var acct, list string
		if err := frows.Scan(&acct, &list); err != nil {
			frows.Close()
			return err
		}
		if set := countrySet(list); len(set) > 0 {
			fences[acct] = set
		}
	}
	frows.Close()
	if err := frows.Err(); err != nil {
		return err
	}
	known := map[string]map[string]bool{}
	krows, err := s.db.QueryContext(ctx, `SELECT account, ip_fp FROM signin_known`)
	if err != nil {
		return err
	}
	for krows.Next() {
		var acct, fp string
		if err := krows.Scan(&acct, &fp); err != nil {
			krows.Close()
			return err
		}
		if known[acct] == nil {
			known[acct] = map[string]bool{}
		}
		known[acct][fp] = true
	}
	krows.Close()
	if err := krows.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	s.blocks, s.fences, s.known = blocks, fences, known
	s.mu.Unlock()
	return nil
}

// ── Addresses ────────────────────────────────────────────────────────────────

// parseIP reads an address given with or without its port.
func parseIP(addr string) net.IP {
	addr = strings.TrimSpace(addr)
	if h, _, err := net.SplitHostPort(addr); err == nil {
		addr = h
	}
	return net.ParseIP(strings.Trim(addr, "[]"))
}

// network is ip's network: its /24, or its /48 for IPv6.
func network(ip net.IP) net.IP {
	if v4 := ip.To4(); v4 != nil {
		return v4.Mask(net.CIDRMask(24, 32))
	}
	return ip.Mask(net.CIDRMask(48, 128))
}

// Mask is ip with its last part hidden: the first two octets of an IPv4
// address, the first two groups of an IPv6 one.
func Mask(ip net.IP) string {
	if v4 := ip.To4(); v4 != nil {
		return strconv.Itoa(int(v4[0])) + "." + strconv.Itoa(int(v4[1])) + ".x.x"
	}
	if ip16 := ip.To16(); ip16 != nil {
		return strconv.FormatUint(uint64(ip16[0])<<8|uint64(ip16[1]), 16) + ":" + strconv.FormatUint(uint64(ip16[2])<<8|uint64(ip16[3]), 16) + ":x:x::"
	}
	return "unknown"
}

// NetworkLabel is how a network block is shown, hiding what Mask hides.
func NetworkLabel(masked string) string {
	if strings.HasSuffix(masked, ".x.x") {
		return strings.TrimSuffix(masked, ".x.x") + ".x.0/24"
	}
	return strings.TrimSuffix(masked, "x::") + ":/48"
}

// fp is the keyed fingerprint of b, with a purpose so an address and a
// network never share one.
func (s *Store) fp(purpose string, b []byte) string {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(purpose))
	m.Write([]byte{0})
	m.Write(b)
	return hex.EncodeToString(m.Sum(nil)[:16])
}

type source struct {
	fp, net, masked, country string
	private                  bool
}

func (s *Store) source(addr string, withCountry bool) (source, bool) {
	ip := parseIP(addr)
	if ip == nil {
		return source{}, false
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	src := source{
		fp:      s.fp("address", ip),
		net:     s.fp("network", network(ip)),
		masked:  Mask(ip),
		private: ip.IsPrivate() || ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast(),
	}
	if withCountry && Country != nil {
		src.country = strings.ToUpper(Country(ip.String()))
	}
	return src, true
}

// Country resolves an address to its ISO country code. The app sets it to
// geoip.Country; nil leaves every country unknown.
var Country func(ip string) string

// ── The gate ─────────────────────────────────────────────────────────────────

// Refuse reports whether a sign-in to account from addr is refused before its
// password is checked: by a block on the address, its network or its country,
// for the install or for the account, or by the account's fence. A refusal is
// counted on its block.
func (s *Store) Refuse(account, addr string) (Refusal, bool) {
	if s == nil {
		return Refusal{}, false
	}
	src, ok := s.source(addr, true)
	if !ok {
		return Refusal{}, false
	}
	now := s.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range s.blocks {
		if !b.Active(now) || (b.Account != "" && b.Account != account) {
			continue
		}
		if (b.Kind == "address" && b.Key == src.fp) || (b.Kind == "network" && b.Key == src.net) ||
			(b.Kind == "country" && src.country != "" && b.Key == src.country) {
			s.hits[b.ID]++
			c := *b
			return Refusal{Block: &c}, true
		}
	}
	// A fence allows only its countries. An address whose country is not
	// known (a private one, or geolocation turned off) is let through: the
	// fence cannot judge it, and refusing it would refuse the holder on
	// their own network.
	if set := s.fences[account]; len(set) > 0 && src.country != "" && !set[src.country] {
		return Refusal{Fence: true}, true
	}
	return Refusal{}, false
}

// ── Recording ────────────────────────────────────────────────────────────────

// Record counts one attempt. A failure feeds the spraying rule; a right
// password with a wrong code raises the alarm.
func (s *Store) Record(a Attempt) {
	if s == nil || a.Account == "" {
		return
	}
	// The account is what the guesser typed; it is kept at an address's
	// longest, so a flood of long names costs the table nothing more.
	if len(a.Account) > 254 {
		a.Account = a.Account[:254]
	}
	src, ok := s.source(a.IP, true)
	if !ok {
		return
	}
	now := s.Now()
	k := pendKey{minute: now.Unix() / 60, account: a.Account, door: a.Door, stage: a.Stage, fp: src.fp}
	s.mu.Lock()
	if v := s.pending[k]; v != nil {
		v.n++
	} else if len(s.pending) < pendingCap {
		s.pending[k] = &pendVal{net: src.net, masked: src.masked, country: src.country, n: 1}
	} else {
		s.dropped++
	}
	known := s.known[a.Account][src.fp]
	var alarm *Alarm
	if a.Stage == StageCode && !known {
		ak := alarmKey{a.Account, src.fp}
		if last, ok := s.alarmed[ak]; !ok || now.Sub(last) >= alarmEvery {
			s.alarmed[ak] = now
			alarm = &Alarm{Account: a.Account, Door: a.Door, Masked: src.masked, Country: src.country, At: now}
		}
	}
	spraying := false
	if (a.Stage == StagePassword || a.Stage == StageCode) && !known && !src.private {
		spraying = s.noteFailureLocked(src.fp, a.Account, now)
	}
	notify := s.OnAlarm
	s.mu.Unlock()
	if alarm != nil && notify != nil {
		go notify(*alarm)
	}
	if spraying {
		s.blockSpraying(src, now)
	}
}

// noteFailureLocked follows a source's failures across accounts and reports
// whether it has now sprayed. Caller holds s.mu.
func (s *Store) noteFailureLocked(fp, account string, now time.Time) bool {
	st := s.spray[fp]
	if st == nil {
		if len(s.spray) >= sprayCap {
			s.trimSprayLocked(now)
			if len(s.spray) >= sprayCap {
				return false
			}
		}
		st = &sprayState{accounts: map[string]time.Time{}}
		s.spray[fp] = st
	}
	for acct, t := range st.accounts {
		if now.Sub(t) >= SprayWindow {
			delete(st.accounts, acct)
		}
	}
	st.accounts[account] = now
	if len(st.accounts) < SprayAccounts {
		return false
	}
	for _, b := range s.blocks {
		if b.Kind == "address" && b.Key == fp && b.Account == "" && b.Active(now) {
			return false
		}
	}
	delete(s.spray, fp)
	return true
}

func (s *Store) trimSprayLocked(now time.Time) {
	for fp, st := range s.spray {
		fresh := false
		for _, t := range st.accounts {
			if now.Sub(t) < SprayWindow {
				fresh = true
				break
			}
		}
		if !fresh {
			delete(s.spray, fp)
		}
	}
}

// blockSpraying blocks a source caught spraying: for SprayFirst, or for twice
// its last such block when that ended less than SprayRepeat ago.
func (s *Store) blockSpraying(src source, now time.Time) {
	wait := SprayFirst
	var lastCreated, lastUntil int64
	err := s.db.QueryRow(`SELECT created, until FROM signin_blocks WHERE kind = 'address' AND key = ? AND reason = 'spraying' ORDER BY until DESC LIMIT 1`, src.fp).Scan(&lastCreated, &lastUntil)
	if err == nil && lastUntil > 0 && now.Sub(time.Unix(lastUntil, 0)) < SprayRepeat {
		if d := 2 * time.Duration(lastUntil-lastCreated) * time.Second; d > wait {
			wait = d
		}
		if wait > SprayMax {
			wait = SprayMax
		}
	}
	_, _ = s.add(context.Background(), &Block{Kind: "address", Key: src.fp, Label: src.masked, Reason: "spraying", Created: now, Until: now.Add(wait)})
}

// Flush writes the counts waiting, the blocks' refusals, and drops attempts
// past Retention, at most hourly. Counts the database refuses are kept for
// the next flush.
func (s *Store) Flush(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	pending, hits := s.pending, s.hits
	s.pending, s.hits = map[pendKey]*pendVal{}, map[int64]int{}
	now := s.Now()
	trim := now.Sub(s.lastTrim) >= time.Hour
	for k, t := range s.alarmed {
		if now.Sub(t) >= alarmEvery {
			delete(s.alarmed, k)
		}
	}
	s.trimSprayLocked(now)
	s.mu.Unlock()
	if len(pending) == 0 && len(hits) == 0 && !trim {
		return nil
	}
	err := s.write(ctx, pending, hits, trim, now)
	if err != nil {
		s.mu.Lock()
		for k, v := range pending {
			if cur := s.pending[k]; cur != nil {
				cur.n += v.n
			} else if len(s.pending) < pendingCap {
				s.pending[k] = v
			} else {
				s.dropped += v.n
			}
		}
		for id, n := range hits {
			s.hits[id] += n
		}
		s.mu.Unlock()
		return err
	}
	if trim {
		s.mu.Lock()
		s.lastTrim = now
		s.mu.Unlock()
	}
	return nil
}

func (s *Store) write(ctx context.Context, pending map[pendKey]*pendVal, hits map[int64]int, trim bool, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if len(pending) > 0 {
		st, err := tx.PrepareContext(ctx, `INSERT INTO signin_attempts (minute, account, door, stage, ip_fp, net_fp, ip_masked, country, n) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (minute, account, door, stage, ip_fp) DO UPDATE SET n = n + excluded.n`)
		if err != nil {
			return err
		}
		defer st.Close()
		for k, v := range pending {
			if _, err := st.ExecContext(ctx, k.minute, k.account, k.door, k.stage, k.fp, v.net, v.masked, v.country, v.n); err != nil {
				return err
			}
		}
	}
	for id, n := range hits {
		if _, err := tx.ExecContext(ctx, `UPDATE signin_blocks SET hits = hits + ? WHERE id = ?`, n, id); err != nil {
			return err
		}
	}
	if trim {
		if _, err := tx.ExecContext(ctx, `DELETE FROM signin_attempts WHERE minute < ?`, now.Add(-Retention).Unix()/60); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM signin_blocks WHERE until > 0 AND until < ?`, now.Add(-Retention).Unix()); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.mu.Lock()
	for _, b := range s.blocks {
		b.Hits += hits[b.ID]
	}
	s.mu.Unlock()
	return nil
}

// Dropped reports the attempts dropped at the cap since the store opened.
func (s *Store) Dropped() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dropped
}

// ── Blocks ───────────────────────────────────────────────────────────────────

func (s *Store) add(ctx context.Context, b *Block) (*Block, error) {
	var until int64
	if !b.Until.IsZero() {
		until = b.Until.Unix()
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO signin_blocks (kind, key, label, account, reason, created, until) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		b.Kind, b.Key, b.Label, b.Account, b.Reason, b.Created.Unix(), until)
	if err != nil {
		return nil, err
	}
	b.ID, _ = res.LastInsertId()
	s.mu.Lock()
	s.blocks = append(s.blocks, b)
	s.mu.Unlock()
	return b, nil
}

// BlockSource blocks the address behind fingerprint fp, or its network when
// network is set, for account ("" for the install), for d (0: always). by
// names who set it. self is the address of the person setting it: a block
// that would refuse them is refused with ErrSelf.
func (s *Store) BlockSource(ctx context.Context, fp string, network bool, account string, d time.Duration, by, self string) (*Block, error) {
	var netFP, masked string
	err := s.db.QueryRowContext(ctx, `SELECT net_fp, ip_masked FROM signin_attempts WHERE ip_fp = ? ORDER BY minute DESC LIMIT 1`, fp).Scan(&netFP, &masked)
	if err != nil {
		return nil, err
	}
	b := &Block{Kind: "address", Key: fp, Label: masked, Account: account, Reason: by, Created: s.Now()}
	if network {
		b.Kind, b.Key, b.Label = "network", netFP, NetworkLabel(masked)
	}
	if d > 0 {
		b.Until = b.Created.Add(d)
	}
	if me, ok := s.source(self, false); ok && (b.Key == me.fp || b.Key == me.net) {
		return nil, ErrSelf
	}
	return s.add(ctx, b)
}

// BlockCountry refuses sign-ins from country, for account or the install.
// Pages stay readable: only sign-in is refused.
func (s *Store) BlockCountry(ctx context.Context, country, account string, d time.Duration, by, self string) (*Block, error) {
	country = strings.ToUpper(strings.TrimSpace(country))
	if len(country) != 2 {
		return nil, errors.New("signin: a country is two letters")
	}
	if me, ok := s.source(self, true); ok && me.country == country {
		return nil, ErrSelf
	}
	b := &Block{Kind: "country", Key: country, Label: country, Account: account, Reason: by, Created: s.Now()}
	if d > 0 {
		b.Until = b.Created.Add(d)
	}
	return s.add(ctx, b)
}

// Lift ends block id now.
func (s *Store) Lift(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE signin_blocks SET lifted = 1 WHERE id = ?`, id); err != nil {
		return err
	}
	s.mu.Lock()
	kept := s.blocks[:0]
	for _, b := range s.blocks {
		if b.ID != id {
			kept = append(kept, b)
		}
	}
	s.blocks = kept
	s.mu.Unlock()
	return nil
}

// Blocks lists the blocks refusing now, newest first; for account, those of
// the install and its own.
func (s *Store) Blocks(account string) []Block {
	now := s.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Block
	for _, b := range s.blocks {
		if b.Active(now) && (account == "" || b.Account == "" || b.Account == account) {
			c := *b
			c.Hits += s.hits[b.ID]
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out
}

// ItWasMe clears source fp for account: its blocks on the install that only
// spraying set are lifted, and the account remembers the place, so its
// failures there no longer count as spraying nor raise the alarm.
func (s *Store) ItWasMe(ctx context.Context, account, fp string) error {
	if _, err := s.db.ExecContext(ctx, `INSERT INTO signin_known (account, ip_fp, at) VALUES (?, ?, ?) ON CONFLICT (account, ip_fp) DO UPDATE SET at = excluded.at`, account, fp, s.Now().Unix()); err != nil {
		return err
	}
	s.mu.Lock()
	if s.known[account] == nil {
		s.known[account] = map[string]bool{}
	}
	s.known[account][fp] = true
	var lift []int64
	for _, b := range s.blocks {
		if b.Kind == "address" && b.Key == fp && b.Reason == "spraying" {
			lift = append(lift, b.ID)
		}
	}
	s.mu.Unlock()
	for _, id := range lift {
		if err := s.Lift(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// ── Fences ───────────────────────────────────────────────────────────────────

func countrySet(list string) map[string]bool {
	set := map[string]bool{}
	for _, c := range strings.Split(list, ",") {
		if c = strings.ToUpper(strings.TrimSpace(c)); len(c) == 2 {
			set[c] = true
		}
	}
	return set
}

// Fence is account's fence: the countries it may sign in from, or none.
func (s *Store) Fence(account string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for c := range s.fences[account] {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// SetFence allows account to sign in only from countries (none: anywhere).
// self is the address of the holder setting it: a fence that would shut them
// out is refused with ErrSelf.
func (s *Store) SetFence(ctx context.Context, account string, countries []string, self string) error {
	set := countrySet(strings.Join(countries, ","))
	if len(set) > 0 {
		if me, ok := s.source(self, true); ok && me.country != "" && !set[me.country] {
			return ErrSelf
		}
	}
	list := make([]string, 0, len(set))
	for c := range set {
		list = append(list, c)
	}
	sort.Strings(list)
	var err error
	if len(list) == 0 {
		_, err = s.db.ExecContext(ctx, `DELETE FROM signin_fences WHERE account = ?`, account)
	} else {
		_, err = s.db.ExecContext(ctx, `INSERT INTO signin_fences (account, countries) VALUES (?, ?) ON CONFLICT (account) DO UPDATE SET countries = excluded.countries`, account, strings.Join(list, ","))
	}
	if err != nil {
		return err
	}
	s.mu.Lock()
	if len(set) == 0 {
		delete(s.fences, account)
	} else {
		s.fences[account] = set
	}
	s.mu.Unlock()
	return nil
}
