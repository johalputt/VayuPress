// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/johalputt/vayupress/internal/geoip"
	"github.com/johalputt/vayupress/internal/settings"
)

const testCityTable = "../../internal/geoip/testdata/city-test.mmdb"

// cityTableApp is an install with the city switch as on says, its data
// directory a temporary one, and fetches that copy the test table there and
// count themselves.
func cityTableApp(t *testing.T, on bool) (*App, string, *int) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE site_settings (scope TEXT NOT NULL DEFAULT '', key TEXT NOT NULL, value TEXT NOT NULL DEFAULT '', updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, PRIMARY KEY(scope,key))`); err != nil {
		t.Fatal(err)
	}
	a := &App{siteSettings: settings.New(db)}
	if on {
		if err := a.siteSettings.SetMany(context.Background(), settings.ForPrimary(), map[string]string{settings.KeyAnalyticsCities: "true"}); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	fetches := 0
	saved := cityTable.dir
	savedFetch, savedNow, savedChild := cityTable.fetch, cityTable.now, cityTable.isSpaceChld
	cityTable.dir = dir
	cityTable.lastTry, cityTable.lastErr = time.Time{}, ""
	cityTable.fetch = func(_ context.Context, _ *http.Client, dir string, _ time.Time) error {
		fetches++
		b, err := os.ReadFile(testCityTable)
		if err != nil {
			return err
		}
		p := filepath.Join(dir, geoip.CityFile)
		if err := os.WriteFile(p, b, 0o600); err != nil {
			return err
		}
		return geoip.LoadCity(p)
	}
	cityTable.isSpaceChld = func() bool { return false }
	t.Cleanup(func() {
		cityTable.dir, cityTable.fetch, cityTable.now, cityTable.isSpaceChld = saved, savedFetch, savedNow, savedChild
		cityTable.lastTry, cityTable.lastErr = time.Time{}, ""
		geoip.UnloadCity()
	})
	return a, dir, &fetches
}

// On, the table is fetched and put in use, and not fetched again while it
// is current; off, it is put out of use and deleted from the data directory.
func TestTheCitySwitchFetchesAndDeletesTheTable(t *testing.T) {
	a, dir, fetches := cityTableApp(t, true)
	ctx := context.Background()
	a.syncCityTable(ctx)
	if loaded, _ := geoip.CityLoaded(); !loaded || *fetches != 1 {
		t.Fatalf("switched on, the table was fetched %d times and loaded %v", *fetches, loaded)
	}
	// The test table's build time is now: a current table is not fetched again.
	a.syncCityTable(ctx)
	if *fetches != 1 {
		t.Errorf("a current table was fetched again (%d fetches)", *fetches)
	}
	// A month and more later, it is.
	cityTable.now = func() time.Time { return time.Now().Add(33 * 24 * time.Hour) }
	a.syncCityTable(ctx)
	if *fetches != 2 {
		t.Errorf("a table 33 days old was not fetched again (%d fetches)", *fetches)
	}
	if err := a.siteSettings.SetMany(ctx, settings.ForPrimary(), map[string]string{settings.KeyAnalyticsCities: "false"}); err != nil {
		t.Fatal(err)
	}
	a.syncCityTable(ctx)
	if loaded, _ := geoip.CityLoaded(); loaded {
		t.Error("switched off, the table is still in use")
	}
	if _, err := os.Stat(filepath.Join(dir, geoip.CityFile)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("switched off, the table is still in the data directory (%v)", err)
	}
}

// Off by default: an install that never touched the switch fetches nothing.
func TestTheCityTableIsOffByDefault(t *testing.T) {
	a, _, fetches := cityTableApp(t, false)
	a.syncCityTable(context.Background())
	if *fetches != 0 {
		t.Errorf("with the switch never set, the table was fetched %d times", *fetches)
	}
}

// A Tor Space never fetches the table, whatever the switch says.
func TestATorSpaceNeverFetchesTheCityTable(t *testing.T) {
	a, _, fetches := cityTableApp(t, true)
	cityTable.isSpaceChld = func() bool { return true }
	a.syncCityTable(context.Background())
	if *fetches != 0 {
		t.Errorf("a Tor Space fetched the table %d times", *fetches)
	}
}

// After a failed fetch the next try waits 6 hours, and Settings says why.
func TestAFailedCityFetchWaitsAndSaysWhy(t *testing.T) {
	a, _, _ := cityTableApp(t, true)
	tries := 0
	cityTable.fetch = func(context.Context, *http.Client, string, time.Time) error {
		tries++
		return errors.New("download.db-ip.com refused the connection")
	}
	ctx := context.Background()
	start := time.Now()
	cityTable.now = func() time.Time { return start }
	a.syncCityTable(ctx)
	cityTable.now = func() time.Time { return start.Add(5 * time.Hour) }
	a.syncCityTable(ctx)
	if tries != 1 {
		t.Errorf("tried %d times within 6 hours of a failure, want 1", tries)
	}
	if got := string(settingsCityTable(ctx, a)); !strings.Contains(got, "Not downloaded: download.db-ip.com refused the connection") {
		t.Errorf("Settings does not say why the table is not downloaded:\n%s", got)
	}
	cityTable.now = func() time.Time { return start.Add(6*time.Hour + time.Minute) }
	a.syncCityTable(ctx)
	if tries != 2 {
		t.Errorf("not tried again after 6 hours (%d tries)", tries)
	}
}

// A visitor's city comes from the table only when the edge names none, and
// only when the table places them in the country the install records for
// them: a city in another country would put one reader in two places.
func TestTheCityTableFillsOnlyWhatTheEdgeLeftAndInItsCountry(t *testing.T) {
	trustLoopbackProxy(t)
	if err := geoip.LoadCity(testCityTable); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(geoip.UnloadCity)
	req := func(country, city string) *http.Request {
		r := proxiedRequest(t, "/api/v1/analytics/collect")
		r.Header.Set("X-Forwarded-For", "198.51.100.7")
		r.Header.Set("CF-IPCountry", country)
		if city != "" {
			r.Header.Set("CF-IPCity", city)
		}
		return r
	}
	if g := geoFromHeaders(req("IN", "")); g.City != "Pune" || g.Region != "Maharashtra" {
		t.Errorf("no city from the edge: got %q, %q, want Pune, Maharashtra from the table", g.City, g.Region)
	}
	if g := geoFromHeaders(req("IN", "Mumbai")); g.City != "Mumbai" {
		t.Errorf("the edge's city was replaced by the table's: %q", g.City)
	}
	if g := geoFromHeaders(req("FR", "")); g.City != "" || g.Region != "" {
		t.Errorf("the table's Indian city was recorded for a French visitor: %q, %q", g.City, g.Region)
	}
}
