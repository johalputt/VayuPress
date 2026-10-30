// SPDX-License-Identifier: Apache-2.0

package geoip

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/oschwald/maxminddb-golang/v2"
)

// The city table: an optional, downloaded companion to the embedded country
// table, for an install with no CDN in front to name a visitor's city.
//
// It is not compiled in. DB-IP's City Lite is about 130 MB and changes every
// month, so embedding it would multiply the binary several times over and
// still be stale by the next release. The install downloads it itself, only
// when the operator turns it on, into its data directory, and reads it from
// there (memory-mapped, so the pages read are the only ones held). Lookups
// stay on this server: no visitor address is sent anywhere, and only the
// city and region names are stored, as with the country.
//
// The data is DB-IP's, under CC BY 4.0, which asks for the credit
// CityAttribution names wherever its cities are shown.

// CityFile is the table's name in the data directory.
const CityFile = "dbip-city-lite.mmdb"

// CityAttribution is the credit CC BY 4.0 asks of DB-IP's data.
const CityAttribution = `<a href="https://db-ip.com" rel="noopener noreferrer" target="_blank">IP Geolocation by DB-IP</a>`

// cityMaxBytes bounds a download, unpacked. The table was 130 MB in 2026; a
// response several times that is not the table. A variable for the test.
var cityMaxBytes int64 = 512 << 20

// cityURL is where DB-IP publishes a month's table. A variable so a test can
// serve its own.
var cityURL = func(month time.Time) string {
	return "https://download.db-ip.com/free/dbip-city-lite-" + month.Format("2006-01") + ".mmdb.gz"
}

var (
	cityMu     sync.RWMutex
	cityReader *maxminddb.Reader
	cityBuilt  time.Time
)

// cityRecord is the part of a City-format record read: DB-IP's carries the
// same paths as MaxMind's City databases.
type cityRecord struct {
	City struct {
		Names map[string]string `maxminddb:"names"`
	} `maxminddb:"city"`
	Country struct {
		ISOCode string `maxminddb:"iso_code"`
	} `maxminddb:"country"`
	Subdivisions []struct {
		Names map[string]string `maxminddb:"names"`
	} `maxminddb:"subdivisions"`
	Location struct {
		Latitude  *float64 `maxminddb:"latitude"`
		Longitude *float64 `maxminddb:"longitude"`
	} `maxminddb:"location"`
}

// Place is where the city table puts an address: its country's code, its
// region and city, and the city's coordinate when the table gives one,
// rounded to a tenth of a degree (about 11 km): the city's own centre as the
// table records it, never a reader's position, and no finer than a map of
// the world needs.
type Place struct {
	Country, Region, City string
	Lat, Lon              float64
	Located               bool
}

// City is where the table places ip, or the zero Place when no table is
// loaded, the address is private or reserved, or the table does not place
// it. Resolution switched off (ANALYTICS_GEOIP) switches this off too.
func City(ip string) Place {
	if !Enabled() {
		return Place{}
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return Place{}
	}
	if addr.IsPrivate() || addr.IsLoopback() || addr.IsUnspecified() || addr.IsLinkLocalUnicast() {
		return Place{}
	}
	cityMu.RLock()
	defer cityMu.RUnlock()
	if cityReader == nil {
		return Place{}
	}
	var rec cityRecord
	if err := cityReader.Lookup(addr).Decode(&rec); err != nil {
		return Place{}
	}
	p := Place{Country: strings.ToUpper(rec.Country.ISOCode), City: rec.City.Names["en"]}
	if len(rec.Subdivisions) > 0 {
		p.Region = rec.Subdivisions[0].Names["en"]
	}
	if lat, lon := rec.Location.Latitude, rec.Location.Longitude; lat != nil && lon != nil {
		p.Lat, p.Lon, p.Located = math.Round(*lat*10)/10, math.Round(*lon*10)/10, true
	}
	return p
}

// CityLoaded reports whether a city table is in use, and when it was built.
func CityLoaded() (bool, time.Time) {
	cityMu.RLock()
	defer cityMu.RUnlock()
	return cityReader != nil, cityBuilt
}

// openCity opens and checks the table at path. A file that is not a whole,
// well-formed City table is refused, so a cut-off or wrong download is never
// put in use.
func openCity(path string) (*maxminddb.Reader, error) {
	r, err := maxminddb.Open(path)
	if err != nil {
		return nil, err
	}
	if !strings.Contains(strings.ToLower(r.Metadata.DatabaseType), "city") {
		r.Close()
		return nil, fmt.Errorf("geoip: %s is a %q table, not a city table", filepath.Base(path), r.Metadata.DatabaseType)
	}
	if err := r.Verify(); err != nil {
		r.Close()
		return nil, fmt.Errorf("geoip: %s is damaged: %w", filepath.Base(path), err)
	}
	return r, nil
}

// LoadCity puts the table at path in use, in place of any before it.
func LoadCity(path string) error {
	r, err := openCity(path)
	if err != nil {
		return err
	}
	cityMu.Lock()
	old := cityReader
	cityReader, cityBuilt = r, r.Metadata.BuildTime()
	cityMu.Unlock()
	if old != nil {
		old.Close()
	}
	return nil
}

// UnloadCity stops using the city table.
func UnloadCity() {
	cityMu.Lock()
	old := cityReader
	cityReader, cityBuilt = nil, time.Time{}
	cityMu.Unlock()
	if old != nil {
		old.Close()
	}
}

// FetchCity downloads the newest month's table into dir and puts it in use.
// DB-IP publishes a month's table early in that month, so before it is out
// the previous month's is fetched. The download is unpacked into a file
// beside the table, checked whole, and only then renamed over it, so a
// failed fetch leaves the table in use as it was.
func FetchCity(ctx context.Context, client *http.Client, dir string, now time.Time) error {
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	err := fetchCityMonth(ctx, client, dir, month)
	if errors.Is(err, errNotPublished) {
		err = fetchCityMonth(ctx, client, dir, month.AddDate(0, -1, 0))
	}
	return err
}

var errNotPublished = errors.New("geoip: that month's table is not published")

func fetchCityMonth(ctx context.Context, client *http.Client, dir string, month time.Time) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cityURL(month), nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return errNotPublished
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("geoip: the city table's download answered %s", resp.Status)
	}
	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return fmt.Errorf("geoip: the city table's download is not gzip: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".dbip-city-*.part")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	n, err := io.Copy(tmp, io.LimitReader(gz, cityMaxBytes+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("geoip: the city table's download failed: %w", err)
	}
	if n > cityMaxBytes {
		return fmt.Errorf("geoip: the city table's download is over %d MB", cityMaxBytes>>20)
	}
	r, err := openCity(tmp.Name())
	if err != nil {
		return err
	}
	r.Close()
	final := filepath.Join(dir, CityFile)
	if err := os.Rename(tmp.Name(), final); err != nil {
		return err
	}
	return LoadCity(final)
}
