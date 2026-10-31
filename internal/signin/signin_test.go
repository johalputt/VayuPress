// SPDX-License-Identifier: Apache-2.0

package signin

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
)

var testKey = []byte("0123456789abcdef0123456789abcdef")

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func openStore(t *testing.T) (*Store, *clock) {
	t.Helper()
	prev := config.Cfg.DBPath
	config.Cfg.DBPath = filepath.Join(t.TempDir(), "signin.db")
	if err := dbpkg.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		dbpkg.ClosePools()
		_ = dbpkg.DB.Close()
		config.Cfg.DBPath = prev
	})
	s, err := Open(context.Background(), dbpkg.DB, testKey)
	if err != nil {
		t.Fatal(err)
	}
	c := &clock{t: time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)}
	s.Now = c.now
	return s, c
}

func withCountries(t *testing.T, m map[string]string) {
	t.Helper()
	prev := Country
	Country = func(ip string) string { return m[ip] }
	t.Cleanup(func() { Country = prev })
}

func fail(s *Store, account, ip string) {
	s.Record(Attempt{Account: account, Door: DoorIMAP, Stage: StagePassword, IP: ip})
}

// The masked form hides the last part of an address and nothing more, and a
// network's label hides what the address's hides.
func TestAnAddressIsShownWithItsLastPartHidden(t *testing.T) {
	for in, want := range map[string]string{
		"103.21.244.7":          "103.21.x.x",
		"::ffff:103.21.244.7":   "103.21.x.x",
		"2001:db8:85a3::8a2e:1": "2001:db8:x:x::",
	} {
		if got := Mask(net.ParseIP(in)); got != want {
			t.Errorf("Mask(%s) = %s, want %s", in, got, want)
		}
	}
	if got := networkLabel("103.21.x.x"); got != "103.21.x.0/24" {
		t.Errorf("network label = %s", got)
	}
	if got := networkLabel("2001:db8:x:x::"); got != "2001:db8:x::/48" {
		t.Errorf("IPv6 network label = %s", got)
	}
}

// Nothing in the database file holds a whole address: not as text, not as
// bytes; and the fingerprint is the key's, so another install's key makes
// another one.
func TestNoWholeAddressIsWrittenDown(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	v4, v6 := "203.0.113.57", "2001:db8:85a3:1:2:3:4:5"
	fail(s, "ann@example.com", v4+":40112")
	fail(s, "ann@example.com", "["+v6+"]:993")
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BlockSource(ctx, s.fp("address", net.ParseIP(v4).To4()), true, "", time.Hour, "admin", "198.51.100.9"); err != nil {
		t.Fatal(err)
	}
	if _, err := dbpkg.DB.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	for _, f := range []string{config.Cfg.DBPath, config.Cfg.DBPath + "-wal"} {
		b, err := os.ReadFile(f)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		raw = append(raw, b...)
	}
	for _, needle := range [][]byte{[]byte(v4), []byte("113.57"), net.ParseIP(v4).To4(), []byte(v6), []byte("85a3:1:2"), net.ParseIP(v6).To16()} {
		if bytes.Contains(raw, needle) {
			t.Errorf("the database file holds %q", needle)
		}
	}
	if !bytes.Contains(raw, []byte("203.0.x.x")) {
		t.Error("the masked address is not in the file; the search read nothing")
	}
	other, _ := Open(ctx, dbpkg.DB, []byte("another install's key, 32 bytes!"))
	if other.fp("address", net.ParseIP(v4).To4()) == s.fp("address", net.ParseIP(v4).To4()) {
		t.Error("two keys made one fingerprint")
	}
}

// A source failing on three accounts within an hour is refused sign-in for
// six hours; caught again within a day of that ending, for twelve, then
// twenty-four, and never longer.
func TestSprayingIsRefusedSixHoursThenTwelveThenADayAtMost(t *testing.T) {
	s, c := openStore(t)
	const ip = "198.51.100.23"
	spray := func() {
		for _, a := range []string{"a@x.test", "b@x.test", "c@x.test"} {
			fail(s, a, ip)
		}
	}
	for i, want := range []time.Duration{6 * time.Hour, 12 * time.Hour, 24 * time.Hour, 24 * time.Hour} {
		spray()
		ref, refused := s.Refuse("d@x.test", ip)
		if !refused || ref.Block == nil {
			t.Fatalf("round %d: a source that failed on three accounts was not refused", i)
		}
		if got := ref.Block.Until.Sub(c.now()); got != want || ref.Block.Reason != "spraying" {
			t.Errorf("round %d: refused for %s (%s), want %s for spraying", i, got, ref.Block.Reason, want)
		}
		spray()
		if n := len(s.Blocks("")); n != 1 {
			t.Fatalf("round %d: spraying while blocked made %d blocks, want the one", i, n)
		}
		c.add(ref.Block.Until.Sub(c.now()) + time.Hour)
		if _, refused := s.Refuse("d@x.test", ip); refused {
			t.Fatalf("round %d: still refused after the block ended", i)
		}
	}
	c.add(SprayRepeat)
	spray()
	if ref, _ := s.Refuse("d@x.test", ip); ref.Block == nil || ref.Block.Until.Sub(c.now()) != SprayFirst {
		t.Errorf("a source caught a day after its last block ended was not back to the first wait: %+v", ref.Block)
	}
}

// Two accounts are not spraying; three outside the hour are not either.
func TestTwoAccountsOrThreeSpreadPastTheHourAreNotSpraying(t *testing.T) {
	s, c := openStore(t)
	const ip = "198.51.100.24"
	fail(s, "a@x.test", ip)
	fail(s, "a@x.test", ip)
	fail(s, "b@x.test", ip)
	if _, refused := s.Refuse("a@x.test", ip); refused {
		t.Fatal("two accounts were taken for spraying")
	}
	c.add(SprayWindow)
	fail(s, "c@x.test", ip)
	if _, refused := s.Refuse("a@x.test", ip); refused {
		t.Fatal("three accounts over more than an hour were taken for spraying")
	}
}

// An address on the operator's own network is never blocked automatically:
// behind a proxy whose address is all the install sees, it is everyone.
func TestAPrivateAddressIsNeverBlockedAutomatically(t *testing.T) {
	s, _ := openStore(t)
	for _, a := range []string{"a@x.test", "b@x.test", "c@x.test", "d@x.test"} {
		fail(s, a, "10.0.0.5")
	}
	if _, refused := s.Refuse("a@x.test", "10.0.0.5"); refused {
		t.Fatal("a private address was blocked for spraying")
	}
}

// "It was me" lifts the spraying block and the place stops counting for that
// account.
func TestItWasMeLiftsTheBlockAndThePlaceStopsCounting(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	const ip = "198.51.100.30"
	for _, a := range []string{"a@x.test", "b@x.test", "c@x.test"} {
		fail(s, a, ip)
	}
	src, _ := s.source(ip, false)
	if err := s.ItWasMe(ctx, "a@x.test", src.fp); err != nil {
		t.Fatal(err)
	}
	if _, refused := s.Refuse("a@x.test", ip); refused {
		t.Fatal("still refused after It was me")
	}
	for i := 0; i < 5; i++ {
		fail(s, "a@x.test", ip)
	}
	fail(s, "b@x.test", ip)
	fail(s, "c@x.test", ip)
	if _, refused := s.Refuse("a@x.test", ip); refused {
		t.Error("failures at a place an account knows counted toward spraying")
	}
	fail(s, "d@x.test", ip)
	if _, refused := s.Refuse("a@x.test", ip); !refused {
		t.Error("a place one account knows was let spray the others")
	}
}

// A right password with a wrong code raises the alarm once an hour per
// source and account; a wrong password raises none.
func TestARightPasswordWithAWrongCodeRaisesTheAlarm(t *testing.T) {
	s, c := openStore(t)
	got := make(chan Alarm, 8)
	s.OnAlarm = func(a Alarm) { got <- a }
	s.Record(Attempt{Account: "ann@x.test", Door: DoorWeb, Stage: StagePassword, IP: "198.51.100.41"})
	select {
	case a := <-got:
		t.Fatalf("an alarm for a wrong password: %+v", a)
	case <-time.After(100 * time.Millisecond):
	}
	s.Record(Attempt{Account: "ann@x.test", Door: DoorWeb, Stage: StagePassword, IP: "198.51.100.40"})
	s.Record(Attempt{Account: "ann@x.test", Door: DoorWeb, Stage: StageCode, IP: "198.51.100.40"})
	s.Record(Attempt{Account: "ann@x.test", Door: DoorWeb, Stage: StageCode, IP: "198.51.100.40"})
	select {
	case a := <-got:
		if a.Account != "ann@x.test" || a.Masked != "198.51.x.x" || a.Door != DoorWeb {
			t.Errorf("alarm = %+v", a)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no alarm for a right password with a wrong code")
	}
	select {
	case a := <-got:
		t.Fatalf("a second alarm within the hour, or one for a wrong password: %+v", a)
	case <-time.After(100 * time.Millisecond):
	}
	c.add(alarmEvery)
	s.Record(Attempt{Account: "ann@x.test", Door: DoorWeb, Stage: StageCode, IP: "198.51.100.40"})
	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("no alarm an hour later")
	}
	c.add(alarmEvery)
	src, _ := s.source("198.51.100.40", false)
	if err := s.ItWasMe(context.Background(), "ann@x.test", src.fp); err != nil {
		t.Fatal(err)
	}
	s.Record(Attempt{Account: "ann@x.test", Door: DoorWeb, Stage: StageCode, IP: "198.51.100.40"})
	select {
	case a := <-got:
		t.Fatalf("an alarm from a place the holder said was them: %+v", a)
	case <-time.After(100 * time.Millisecond):
	}
}

// An address block refuses that address and not its neighbour; a network
// block refuses the neighbour; a country block refuses its country; an
// account's block refuses that account alone; a lifted or ended block
// refuses nothing.
func TestEachBlockRefusesWhatItNames(t *testing.T) {
	s, c := openStore(t)
	ctx := context.Background()
	withCountries(t, map[string]string{"198.51.100.50": "VN", "198.51.100.51": "VN", "192.0.2.9": "IN"})
	fail(s, "a@x.test", "198.51.100.50")
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	src, _ := s.source("198.51.100.50", false)
	addr, err := s.BlockSource(ctx, src.fp, false, "", time.Hour, "admin", "192.0.2.9")
	if err != nil {
		t.Fatal(err)
	}
	if _, r := s.Refuse("a@x.test", "198.51.100.50"); !r {
		t.Error("an address block did not refuse its address")
	}
	if _, r := s.Refuse("a@x.test", "198.51.100.51"); r {
		t.Error("an address block refused its neighbour")
	}
	netb, err := s.BlockSource(ctx, src.fp, true, "", 0, "admin", "192.0.2.9")
	if err != nil {
		t.Fatal(err)
	}
	if netb.Label != "198.51.x.0/24" {
		t.Errorf("network label = %s", netb.Label)
	}
	if _, r := s.Refuse("a@x.test", "198.51.100.51"); !r {
		t.Error("a network block did not refuse an address in its /24")
	}
	if _, r := s.Refuse("a@x.test", "198.51.101.51"); r {
		t.Error("a network block refused an address outside its /24")
	}
	if err := s.Lift(ctx, netb.ID); err != nil {
		t.Fatal(err)
	}
	if _, r := s.Refuse("a@x.test", "198.51.100.51"); r {
		t.Error("a lifted block still refused")
	}
	cb, err := s.BlockCountry(ctx, "vn", "b@x.test", 0, "b@x.test", "192.0.2.9")
	if err != nil {
		t.Fatal(err)
	}
	if _, r := s.Refuse("b@x.test", "198.51.100.51"); !r {
		t.Error("an account's country block did not refuse its country")
	}
	if _, r := s.Refuse("a@x.test", "198.51.100.51"); r {
		t.Error("an account's country block refused another account")
	}
	_ = s.Lift(ctx, cb.ID)
	c.add(time.Hour)
	if _, r := s.Refuse("a@x.test", "198.51.100.50"); r {
		t.Errorf("block %d refused after it ended", addr.ID)
	}
}

// A block that would refuse the person setting it is refused: their
// address, their network, their country.
func TestABlockThatWouldStopItsMakerIsRefused(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	withCountries(t, map[string]string{"198.51.100.60": "IN", "198.51.100.61": "IN"})
	fail(s, "a@x.test", "198.51.100.60")
	_ = s.Flush(ctx)
	src, _ := s.source("198.51.100.60", false)
	if _, err := s.BlockSource(ctx, src.fp, false, "", 0, "admin", "198.51.100.60"); !errors.Is(err, ErrSelf) {
		t.Errorf("blocking your own address: %v, want ErrSelf", err)
	}
	if _, err := s.BlockSource(ctx, src.fp, true, "", 0, "admin", "198.51.100.61"); !errors.Is(err, ErrSelf) {
		t.Errorf("blocking your own network: %v, want ErrSelf", err)
	}
	if _, err := s.BlockCountry(ctx, "IN", "", 0, "admin", "198.51.100.61"); !errors.Is(err, ErrSelf) {
		t.Errorf("blocking your own country: %v, want ErrSelf", err)
	}
	if err := s.SetFence(ctx, "a@x.test", []string{"DE"}, "198.51.100.61"); !errors.Is(err, ErrSelf) {
		t.Errorf("a fence leaving you out: %v, want ErrSelf", err)
	}
}

// A fence lets its countries through, refuses the others, and lets through
// an address whose country is unknown.
func TestAFenceAllowsOnlyItsCountries(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	withCountries(t, map[string]string{"192.0.2.10": "IN", "198.51.100.70": "VN"})
	if err := s.SetFence(ctx, "a@x.test", []string{"in"}, "192.0.2.10"); err != nil {
		t.Fatal(err)
	}
	if _, r := s.Refuse("a@x.test", "192.0.2.10"); r {
		t.Error("the fence refused its own country")
	}
	if ref, r := s.Refuse("a@x.test", "198.51.100.70"); !r || !ref.Fence {
		t.Error("the fence let another country through")
	}
	if _, r := s.Refuse("a@x.test", "203.0.113.1"); r {
		t.Error("the fence refused an address of unknown country")
	}
	if _, r := s.Refuse("b@x.test", "198.51.100.70"); r {
		t.Error("one account's fence refused another account")
	}
	if got := s.Fence("a@x.test"); strings.Join(got, ",") != "IN" {
		t.Errorf("fence = %v", got)
	}
}

// Attempts are counted per minute and source; a count the database refuses
// is kept for the next flush; past the cap a new count is dropped; attempts
// past the retention go.
func TestAttemptsAreCountedKeptThroughARefusalAndDropped(t *testing.T) {
	s, c := openStore(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		fail(s, "a@x.test", "198.51.100.80")
	}
	if _, err := dbpkg.DB.Exec(`ALTER TABLE signin_attempts RENAME TO away`); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(ctx); err == nil {
		t.Fatal("a refused flush reported no error")
	}
	_, _ = dbpkg.DB.Exec(`ALTER TABLE away RENAME TO signin_attempts`)
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	var rows, n int
	_ = dbpkg.DB.QueryRow(`SELECT COUNT(1), SUM(n) FROM signin_attempts`).Scan(&rows, &n)
	if rows != 1 || n != 3 {
		t.Errorf("%d rows counting %d, want one row counting 3", rows, n)
	}
	for i := 0; i < pendingCap; i++ {
		s.Record(Attempt{Account: "acct" + strconv.Itoa(i), Door: DoorIMAP, Stage: StagePaused, IP: "10.1.2.3"})
	}
	fail(s, "past-the-cap@x.test", "10.1.2.3")
	if s.Dropped() != 1 {
		t.Errorf("%d dropped at the cap, want 1", s.Dropped())
	}
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	c.add(Retention + 2*time.Hour)
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	_ = dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM signin_attempts`).Scan(&rows)
	if rows != 0 {
		t.Errorf("%d rows kept past the retention", rows)
	}
}

// The report counts failures, sources and accounts, groups a source's
// attempts, names the one that knew a password, and leaves sign-ins out.
func TestTheReportAddsUpTheFailures(t *testing.T) {
	s, c := openStore(t)
	ctx := context.Background()
	withCountries(t, map[string]string{"198.51.100.90": "VN", "192.0.2.91": "DE"})
	since := c.now()
	for _, a := range []string{"a@x.test", "a@x.test", "b@x.test"} {
		fail(s, a, "198.51.100.90")
	}
	fail(s, "b@x.test", "203.0.113.93")
	s.Record(Attempt{Account: "a@x.test", Door: DoorWeb, Stage: StageCode, IP: "192.0.2.91"})
	s.Record(Attempt{Account: "a@x.test", Door: DoorWeb, Stage: StageOK, IP: "192.0.2.92"})
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	r, err := s.Report(ctx, since, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if r.Failures != 5 || r.Sources != 3 || r.Accounts != 2 || r.PasswordKnown != 1 {
		t.Errorf("report = %d failures, %d sources, %d accounts, %d knew the password; want 5, 3, 2, 1", r.Failures, r.Sources, r.Accounts, r.PasswordKnown)
	}
	if len(r.Top) != 3 || r.Top[0].Masked != "198.51.x.x" || r.Top[0].Failures != 3 || r.Top[0].Accounts != 2 || r.Top[0].Country != "VN" {
		t.Errorf("first source = %+v", r.Top)
	}
	byMask := map[string]Source{}
	for _, x := range r.Top {
		byMask[x.Masked] = x
	}
	if !byMask["192.0.x.x"].Known || byMask["203.0.x.x"].Known {
		t.Error("the source with the right password is not the one marked")
	}
	if err := s.ItWasMe(ctx, "a@x.test", byMask["192.0.x.x"].FP); err != nil {
		t.Fatal(err)
	}
	if again, _ := s.Report(ctx, since, "", 10); len(again.Top) != 3 || !func() bool {
		for _, x := range again.Top {
			if x.Mine != (x.Masked == "192.0.x.x") {
				return false
			}
		}
		return true
	}() {
		t.Errorf("after It was me, the sources = %+v; want the one marked as the holder's", again.Top)
	}
	if r.Countries["VN"] != 3 || r.Hours[12] != 5 {
		t.Errorf("countries %v, noon %d", r.Countries, r.Hours[12])
	}
	own, _ := s.Report(ctx, since, "b@x.test", 10)
	if own.Failures != 2 {
		t.Errorf("b's own report counts %d, want 2", own.Failures)
	}
	rows, err := s.Attempts(ctx, "", "b@x.test", 10)
	if err != nil || len(rows) != 2 {
		t.Fatalf("b's attempts = %+v, %v; want its 2", rows, err)
	}
	for _, x := range rows {
		if x.Account != "b@x.test" {
			t.Errorf("b's attempts hold %s's", x.Account)
		}
	}
	vn := byMask["198.51.x.x"].FP
	rows, _ = s.Attempts(ctx, vn, "", 10)
	if len(rows) != 2 || rows[0].FP != vn || rows[1].FP != vn {
		t.Errorf("one source's attempts = %+v; want its two counts", rows)
	}
}
