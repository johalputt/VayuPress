// SPDX-License-Identifier: Apache-2.0

package main

// city_table.go — keeps the downloaded city table (geoip.FetchCity) in step
// with its switch in Settings › Advanced: fetched when it is turned on and
// each month after, deleted when it is turned off.

import (
	"context"
	"errors"
	"html"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/geoip"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/ui"
	"github.com/johalputt/vayupress/internal/vayuos/torspace"
)

const (
	// cityRefreshAge is how old a table is fetched again at: DB-IP publishes
	// one a month.
	cityRefreshAge = 32 * 24 * time.Hour
	// cityRetryAfter spaces the attempts after a failed fetch, so a host that
	// cannot reach DB-IP does not try every minute.
	cityRetryAfter = 6 * time.Hour
)

// cityTable is the switch's state as the loop last saw it, for Settings.
var cityTable struct {
	sync.Mutex
	fetching    bool
	lastTry     time.Time
	lastErr     string
	client      *http.Client
	dir         string
	now         func() time.Time
	isSpaceChld func() bool
	fetch       func(ctx context.Context, client *http.Client, dir string, now time.Time) error
}

func init() {
	cityTable.client = &http.Client{Timeout: 20 * time.Minute}
	cityTable.now = time.Now
	cityTable.isSpaceChld = torspace.IsSpaceChild
	cityTable.fetch = geoip.FetchCity
}

func cityTableDir() string {
	if cityTable.dir != "" {
		return cityTable.dir
	}
	return filepath.Dir(config.Cfg.DBPath)
}

func (a *App) citiesOn(ctx context.Context) bool {
	return a.savedSetting(ctx, settings.KeyAnalyticsCities) == "true"
}

// startCityTable checks the switch once a minute, so turning it on fetches
// the table within the minute, until done.
func (a *App) startCityTable(done <-chan struct{}) {
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			a.syncCityTable(context.Background())
			select {
			case <-done:
				return
			case <-t.C:
			}
		}
	}()
}

// syncCityTable brings the table in line with the switch. A Tor Space never
// fetches it: its readers' addresses are Tor's, so it would place no one,
// and the Space makes no request to the clear web of its own.
func (a *App) syncCityTable(ctx context.Context) {
	if cityTable.isSpaceChld() {
		return
	}
	path := filepath.Join(cityTableDir(), geoip.CityFile)
	if !a.citiesOn(ctx) {
		if loaded, _ := geoip.CityLoaded(); loaded {
			geoip.UnloadCity()
		}
		if err := os.Remove(path); err == nil {
			logging.LogInfo("geoip", "city table deleted: the switch is off")
		}
		cityTable.Lock()
		cityTable.lastErr, cityTable.lastTry = "", time.Time{}
		cityTable.Unlock()
		return
	}
	loaded, built := geoip.CityLoaded()
	if !loaded {
		if err := geoip.LoadCity(path); err == nil {
			loaded, built = geoip.CityLoaded()
		} else if !errors.Is(err, os.ErrNotExist) {
			logging.LogInfo("geoip", "city table on disk refused, fetching it again: "+err.Error())
		}
	}
	now := cityTable.now()
	if loaded && now.Sub(built) < cityRefreshAge {
		return
	}
	cityTable.Lock()
	if cityTable.fetching || (!cityTable.lastTry.IsZero() && now.Sub(cityTable.lastTry) < cityRetryAfter) {
		cityTable.Unlock()
		return
	}
	cityTable.fetching, cityTable.lastTry = true, now
	cityTable.Unlock()

	err := cityTable.fetch(ctx, cityTable.client, cityTableDir(), now)
	cityTable.Lock()
	cityTable.fetching = false
	cityTable.lastErr = ""
	if err != nil {
		cityTable.lastErr = err.Error()
	}
	cityTable.Unlock()
	if err != nil {
		logging.LogInfo("geoip", "city table not fetched, next try in 6 hours: "+err.Error())
		return
	}
	logging.LogInfo("geoip", "city table fetched")
}

// tableCity is where the city table places ip, when it
// places it in country, the country every other part of the install uses
// for this visitor (requestCountry). A city in another country than the one
// recorded would put the reader in two places at once, so it is left out.
func tableCity(ip, country string) geoip.Place {
	p := geoip.City(ip)
	if p.Country == "" || p.Country != country {
		return geoip.Place{}
	}
	return p
}

// settingsCityTable is the switch, with what the table is doing now.
func settingsCityTable(ctx context.Context, a *App) ui.HTML {
	state := "Off. Cities are named only when a proxy in front of the install sends them."
	if a.citiesOn(ctx) {
		cityTable.Lock()
		fetching, lastErr := cityTable.fetching, cityTable.lastErr
		cityTable.Unlock()
		loaded, built := geoip.CityLoaded()
		switch {
		case cityTable.isSpaceChld():
			state = "Not used in a Tor Space."
		case fetching:
			state = "Downloading the table now."
		case loaded:
			state = "In use: DB-IP's table of " + built.UTC().Format("January 2006") + ". " + geoip.CityAttribution + "."
		case lastErr != "":
			state = "Not downloaded: " + html.EscapeString(lastErr) + ". It is tried again every 6 hours."
		default:
			state = "Waiting to download the table; it starts within a minute."
		}
	}
	f := settingField{ID: "s-cities", Key: settings.KeyAnalyticsCities, Label: "Name visitors' cities", Kind: "toggle", Default: "false", Instant: true}
	hint := "On, this install downloads DB-IP's free city table (CC BY 4.0, about 130 MB, into its data directory, again each month) and Analytics names the city and region of visitors no proxy header places. " +
		"Addresses are looked up on this server and never sent anywhere; only the city and region names are kept. Off, the table is deleted. Saved as you switch it."
	return ui.Join(
		ui.Rows(ui.Row{Label: f.Label, Hint: hint, Control: settingControl(f, a.savedSetting(ctx, f.Key)), ID: f.ID}),
		ui.HTML(`<p class="settings-panel__lead" data-city-state>`+state+`</p>`),
	)
}
