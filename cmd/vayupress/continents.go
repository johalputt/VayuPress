package main

// continents.go — offline ISO 3166-1 alpha-2 → continent mapping for the
// Analytics Geography tab. VayuPress already resolves visitor countries offline
// (internal/geoip, no external service, no stored IP); grouping those country
// codes into continents needs no extra data or lookups, so the Geography tab can
// show a genuine world-region breakdown even when a proxy supplies no
// region/city headers. Render-only, like country names/flags — nothing extra is
// stored, preserving the privacy-by-architecture stance.

import "strings"

// continentName returns the continent for an ISO 3166-1 alpha-2 country code, or
// "" when the code is unknown. Codes are matched case-insensitively.
func continentName(code string) string {
	if c, ok := countryContinent[strings.ToUpper(strings.TrimSpace(code))]; ok {
		return c
	}
	return ""
}

// countryContinent maps ISO alpha-2 codes to one of six inhabited continents
// (Antarctica is folded into "" as it has no resident population that shows up
// in web analytics). The list is deliberately exhaustive so real-world traffic
// from any country groups correctly.
var countryContinent = map[string]string{
	// ── Africa ──
	"DZ": "Africa", "AO": "Africa", "BJ": "Africa", "BW": "Africa", "BF": "Africa",
	"BI": "Africa", "CM": "Africa", "CV": "Africa", "CF": "Africa", "TD": "Africa",
	"KM": "Africa", "CG": "Africa", "CD": "Africa", "CI": "Africa", "DJ": "Africa",
	"EG": "Africa", "GQ": "Africa", "ER": "Africa", "SZ": "Africa", "ET": "Africa",
	"GA": "Africa", "GM": "Africa", "GH": "Africa", "GN": "Africa", "GW": "Africa",
	"KE": "Africa", "LS": "Africa", "LR": "Africa", "LY": "Africa", "MG": "Africa",
	"MW": "Africa", "ML": "Africa", "MR": "Africa", "MU": "Africa", "YT": "Africa",
	"MA": "Africa", "MZ": "Africa", "NA": "Africa", "NE": "Africa", "NG": "Africa",
	"RE": "Africa", "RW": "Africa", "SH": "Africa", "ST": "Africa", "SN": "Africa",
	"SC": "Africa", "SL": "Africa", "SO": "Africa", "ZA": "Africa", "SS": "Africa",
	"SD": "Africa", "TZ": "Africa", "TG": "Africa", "TN": "Africa", "UG": "Africa",
	"EH": "Africa", "ZM": "Africa", "ZW": "Africa",

	// ── Asia ──
	"AF": "Asia", "AM": "Asia", "AZ": "Asia", "BH": "Asia", "BD": "Asia",
	"BT": "Asia", "BN": "Asia", "KH": "Asia", "CN": "Asia", "CY": "Asia",
	"GE": "Asia", "HK": "Asia", "IN": "Asia", "ID": "Asia", "IR": "Asia",
	"IQ": "Asia", "IL": "Asia", "JP": "Asia", "JO": "Asia", "KZ": "Asia",
	"KW": "Asia", "KG": "Asia", "LA": "Asia", "LB": "Asia", "MO": "Asia",
	"MY": "Asia", "MV": "Asia", "MN": "Asia", "MM": "Asia", "NP": "Asia",
	"KP": "Asia", "OM": "Asia", "PK": "Asia", "PS": "Asia", "PH": "Asia",
	"QA": "Asia", "SA": "Asia", "SG": "Asia", "KR": "Asia", "LK": "Asia",
	"SY": "Asia", "TW": "Asia", "TJ": "Asia", "TH": "Asia", "TL": "Asia",
	"TR": "Asia", "TM": "Asia", "AE": "Asia", "UZ": "Asia", "VN": "Asia",
	"YE": "Asia",

	// ── Europe ──
	"AL": "Europe", "AD": "Europe", "AT": "Europe", "BY": "Europe", "BE": "Europe",
	"BA": "Europe", "BG": "Europe", "HR": "Europe", "CZ": "Europe", "DK": "Europe",
	"EE": "Europe", "FO": "Europe", "FI": "Europe", "FR": "Europe", "DE": "Europe",
	"GI": "Europe", "GR": "Europe", "GG": "Europe", "HU": "Europe", "IS": "Europe",
	"IE": "Europe", "IM": "Europe", "IT": "Europe", "JE": "Europe", "XK": "Europe",
	"LV": "Europe", "LI": "Europe", "LT": "Europe", "LU": "Europe", "MT": "Europe",
	"MD": "Europe", "MC": "Europe", "ME": "Europe", "NL": "Europe", "MK": "Europe",
	"NO": "Europe", "PL": "Europe", "PT": "Europe", "RO": "Europe", "RU": "Europe",
	"SM": "Europe", "RS": "Europe", "SK": "Europe", "SI": "Europe", "ES": "Europe",
	"SE": "Europe", "CH": "Europe", "UA": "Europe", "GB": "Europe", "VA": "Europe",
	"AX": "Europe",

	// ── North America ──
	"AI": "North America", "AG": "North America", "AW": "North America", "BS": "North America",
	"BB": "North America", "BZ": "North America", "BM": "North America", "BQ": "North America",
	"VG": "North America", "CA": "North America", "KY": "North America", "CR": "North America",
	"CU": "North America", "CW": "North America", "DM": "North America", "DO": "North America",
	"SV": "North America", "GL": "North America", "GD": "North America", "GP": "North America",
	"GT": "North America", "HT": "North America", "HN": "North America", "JM": "North America",
	"MQ": "North America", "MX": "North America", "MS": "North America", "NI": "North America",
	"PA": "North America", "PR": "North America", "BL": "North America", "KN": "North America",
	"LC": "North America", "MF": "North America", "PM": "North America", "VC": "North America",
	"SX": "North America", "TT": "North America", "TC": "North America", "US": "North America",
	"VI": "North America",

	// ── South America ──
	"AR": "South America", "BO": "South America", "BR": "South America", "CL": "South America",
	"CO": "South America", "EC": "South America", "FK": "South America", "GF": "South America",
	"GY": "South America", "PY": "South America", "PE": "South America", "SR": "South America",
	"UY": "South America", "VE": "South America",

	// ── Oceania ──
	"AS": "Oceania", "AU": "Oceania", "CK": "Oceania", "FJ": "Oceania", "PF": "Oceania",
	"GU": "Oceania", "KI": "Oceania", "MH": "Oceania", "FM": "Oceania", "NR": "Oceania",
	"NC": "Oceania", "NZ": "Oceania", "NU": "Oceania", "NF": "Oceania", "MP": "Oceania",
	"PW": "Oceania", "PG": "Oceania", "WS": "Oceania", "SB": "Oceania", "TK": "Oceania",
	"TO": "Oceania", "TV": "Oceania", "VU": "Oceania", "WF": "Oceania",
}
