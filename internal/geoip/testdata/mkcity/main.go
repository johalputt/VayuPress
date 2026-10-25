// SPDX-License-Identifier: Apache-2.0

// Writes testdata/city-test.mmdb, the City-format table the geoip tests
// read (go run . ../city-test.mmdb, from this directory): a few
// documentation networks, each with the fields DB-IP City Lite carries.
package main

import (
	"log"
	"net"
	"os"

	"github.com/maxmind/mmdbwriter"
	"github.com/maxmind/mmdbwriter/mmdbtype"
)

func place(cc, country, region, city string) mmdbtype.Map {
	return mmdbtype.Map{
		"city":         mmdbtype.Map{"names": mmdbtype.Map{"en": mmdbtype.String(city)}},
		"country":      mmdbtype.Map{"iso_code": mmdbtype.String(cc), "names": mmdbtype.Map{"en": mmdbtype.String(country)}},
		"subdivisions": mmdbtype.Slice{mmdbtype.Map{"names": mmdbtype.Map{"en": mmdbtype.String(region)}}},
	}
}

func main() {
	w, err := mmdbwriter.New(mmdbwriter.Options{DatabaseType: "DBIP-City-Lite", RecordSize: 24, Languages: []string{"en"}, Description: map[string]string{"en": "VayuPress geoip test table"}, IncludeReservedNetworks: true})
	if err != nil {
		log.Fatal(err)
	}
	for cidr, rec := range map[string]mmdbtype.Map{
		"198.51.100.0/25":   place("IN", "India", "Maharashtra", "Pune"),
		"198.51.100.128/25": place("FR", "France", "Île-de-France", "Paris"),
		"203.0.113.0/24":    place("US", "United States", "Texas", "Paris"),
		"2001:db8::/32":     place("DE", "Germany", "Berlin", "Berlin"),
		// A private network a table should never place, so the lookup's own
		// refusal of private addresses is what keeps it out.
		"10.0.0.0/8": place("GB", "United Kingdom", "England", "London"),
	} {
		_, n, _ := net.ParseCIDR(cidr)
		if err := w.Insert(n, rec); err != nil {
			log.Fatal(err)
		}
	}
	f, err := os.Create(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	if _, err := w.WriteTo(f); err != nil {
		log.Fatal(err)
	}
	f.Close()
}
