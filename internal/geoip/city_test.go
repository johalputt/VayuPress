// SPDX-License-Identifier: Apache-2.0

package geoip

import (
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testdata/city-test.mmdb (made by testdata/mkcity) is a City-format table
// of documentation networks: 198.51.100.0/25 Pune, India; 198.51.100.128/25
// Paris, France; 203.0.113.0/24 Paris, Texas; 2001:db8::/32 Berlin; and
// 10.0.0.0/8 London, a private network no lookup may answer for.

func useTestCity(t *testing.T) {
	t.Helper()
	if err := LoadCity("testdata/city-test.mmdb"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(UnloadCity)
}

// A loaded table names the country, region and city of an address, and a
// city by its own country: Paris, France and Paris, Texas are told apart.
func TestTheCityTableNamesWhereAnAddressIs(t *testing.T) {
	if c, r, ci := City("198.51.100.7"); c != "" || r != "" || ci != "" {
		t.Fatalf("with no table loaded, City answered %q %q %q", c, r, ci)
	}
	useTestCity(t)
	for ip, want := range map[string][3]string{
		"198.51.100.7":       {"IN", "Maharashtra", "Pune"},
		"198.51.100.200":     {"FR", "Île-de-France", "Paris"},
		"203.0.113.9":        {"US", "Texas", "Paris"},
		"2001:db8::1":        {"DE", "Berlin", "Berlin"},
		"::ffff:203.0.113.9": {"US", "Texas", "Paris"},
	} {
		c, r, ci := City(ip)
		if [3]string{c, r, ci} != want {
			t.Errorf("City(%s) = %q %q %q, want %q", ip, c, r, ci, want)
		}
	}
	for _, ip := range []string{"10.0.0.1", "::ffff:10.0.0.1", "127.0.0.1", "192.0.2.1", "not an address"} {
		if c, _, ci := City(ip); c != "" || ci != "" {
			t.Errorf("City(%s) placed it in %q %q", ip, ci, c)
		}
	}
	t.Setenv("ANALYTICS_GEOIP", "off")
	if _, _, ci := City("198.51.100.7"); ci != "" {
		t.Errorf("with resolution switched off, City still answered %q", ci)
	}
}

// A damaged file, or a table that is not a city table, is refused and the
// table in use stays.
func TestADamagedCityTableIsRefused(t *testing.T) {
	useTestCity(t)
	good, err := os.ReadFile("testdata/city-test.mmdb")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cut := filepath.Join(dir, "cut.mmdb")
	if err := os.WriteFile(cut, good[:len(good)/2], 0o600); err != nil {
		t.Fatal(err)
	}
	if err := LoadCity(cut); err == nil {
		t.Error("a table cut in half was put in use")
	}
	// Whole metadata over a damaged search tree: it opens, and only the
	// check of the whole file finds it.
	broken := append([]byte{}, good...)
	for i := 0; i < 16; i++ {
		broken[i] = 0xff
	}
	damaged := filepath.Join(dir, "damaged.mmdb")
	if err := os.WriteFile(damaged, broken, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := LoadCity(damaged); err == nil || !strings.Contains(err.Error(), "is damaged") {
		t.Errorf("a table with a damaged tree was not refused as damaged: %v", err)
	}
	country := filepath.Join(dir, "country.mmdb")
	if err := os.WriteFile(country, bytes.Replace(good, []byte("DBIP-City-Lite"), []byte("DBIP-Ctry-Lite"), 1), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := LoadCity(country); err == nil || !strings.Contains(err.Error(), "not a city table") {
		t.Errorf("a country table was not refused as one: %v", err)
	}
	if _, _, ci := City("198.51.100.7"); ci != "Pune" {
		t.Errorf("a refused table displaced the one in use (City = %q)", ci)
	}
}

// cityServer serves the test table, gzipped, as the month named, and 404s
// every other month.
func cityServer(t *testing.T, month string, body []byte) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/"+month+".mmdb.gz") {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	old := cityURL
	cityURL = func(m time.Time) string { return srv.URL + "/" + m.Format("2006-01") + ".mmdb.gz" }
	t.Cleanup(func() { cityURL = old })
}

func gzipped(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write(b)
	_ = zw.Close()
	return buf.Bytes()
}

// The newest month's table is fetched, and before it is published the
// month before's; it lands in the data directory and is put in use.
func TestTheCityTableIsFetchedFromTheNewestMonth(t *testing.T) {
	t.Cleanup(UnloadCity)
	good, _ := os.ReadFile("testdata/city-test.mmdb")
	cityServer(t, "2026-09", gzipped(t, good))
	dir := t.TempDir()
	if err := FetchCity(context.Background(), http.DefaultClient, dir, time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("the month before's table was not fetched: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, CityFile)); err != nil {
		t.Errorf("the table is not in the data directory: %v", err)
	}
	if _, _, ci := City("198.51.100.7"); ci != "Pune" {
		t.Errorf("the fetched table is not in use (City = %q)", ci)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".dbip-city-*")); len(left) != 0 {
		t.Errorf("the download left %v behind", left)
	}
}

// A download that is cut off, too large or not a table, or one a server
// answers with an error, is refused, leaves nothing behind and keeps the
// table in use. An endless stream is cut off at the size bound rather than
// filling the disk.
func TestABadDownloadKeepsTheTableInUse(t *testing.T) {
	useTestCity(t)
	good, _ := os.ReadFile("testdata/city-test.mmdb")
	now := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	serve := func(status int, body []byte) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write(body)
		}
	}
	endless := func(w http.ResponseWriter, r *http.Request) {
		zw := gzip.NewWriter(w)
		zeros := make([]byte, 64<<10)
		for {
			if _, err := zw.Write(zeros); err != nil {
				return
			}
		}
	}
	for name, c := range map[string]struct {
		h   http.HandlerFunc
		why string
	}{
		"cut off":           {serve(200, gzipped(t, good)[:200]), "download failed"},
		"not gzip":          {serve(200, good), "not gzip"},
		"not a table":       {serve(200, gzipped(t, []byte("<html>rate limited</html>"))), "invalid MaxMind DB"},
		"over the size":     {serve(200, gzipped(t, append(append([]byte{}, good...), make([]byte, 4096)...))), "is over"},
		"endless":           {endless, "is over"},
		"an error, a table": {serve(http.StatusServiceUnavailable, gzipped(t, good)), "answered 503"},
	} {
		t.Run(name, func(t *testing.T) {
			old := cityMaxBytes
			cityMaxBytes = int64(len(good)) + 1024
			t.Cleanup(func() { cityMaxBytes = old })
			srv := httptest.NewServer(c.h)
			t.Cleanup(srv.Close)
			oldURL := cityURL
			cityURL = func(time.Time) string { return srv.URL + "/t.mmdb.gz" }
			t.Cleanup(func() { cityURL = oldURL })
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			dir := t.TempDir()
			if err := FetchCity(ctx, http.DefaultClient, dir, now); err == nil || !strings.Contains(err.Error(), c.why) {
				t.Fatalf("the download was not refused as %q: %v", c.why, err)
			}
			if left, _ := os.ReadDir(dir); len(left) != 0 {
				t.Errorf("the refused download left %v behind", left)
			}
			if _, _, ci := City("198.51.100.7"); ci != "Pune" {
				t.Errorf("the refused download displaced the table in use (City = %q)", ci)
			}
		})
	}
}
