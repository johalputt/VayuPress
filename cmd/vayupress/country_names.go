package main

// country_names.go — display helpers for the VayuAnalytics dashboard.
//
// VayuPress stores only the ISO 3166-1 alpha-2 country code supplied by the
// operator's reverse proxy (e.g. Cloudflare's CF-IPCountry); it performs no
// GeoIP lookup and persists no IP. These helpers turn that raw two-letter code
// into a human-friendly country name plus a self-hosted SVG flag *at render
// time only* — nothing extra is stored, preserving the privacy-by-architecture
// stance. Flags are served from /os/static/flags/<cc>.svg (flag-icons, MIT).

import (
	"html"
	"strings"
)

// countryName resolves an ISO 3166-1 alpha-2 code to a full English country
// name. Unknown codes are returned uppercased and unchanged so the dashboard
// still shows something meaningful.
func countryName(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	if n, ok := countryNames[code]; ok {
		return n
	}
	return code
}

// isFlagFile reports whether name is exactly "<cc>.svg" for a two-letter
// lowercase ISO code. Used to bound the public flag route to safe filenames.
func isFlagFile(name string) bool {
	if len(name) != 6 || !strings.HasSuffix(name, ".svg") {
		return false
	}
	for _, c := range name[:2] {
		if c < 'a' || c > 'z' {
			return false
		}
	}
	return true
}

// flagAvailable reports whether an embedded flag SVG exists for the (lowercase)
// two-letter code.
func flagAvailable(lc string) bool {
	return flagCodeSet[lc]
}

// countryFlagURL returns the served path of a country's flag SVG, or "" when no
// flag is available for the code.
func countryFlagURL(code string) string {
	lc := strings.ToLower(strings.TrimSpace(code))
	if len(lc) != 2 || !flagAvailable(lc) {
		return ""
	}
	return "/os/static/flags/" + lc + ".svg"
}

// countryFlagImg returns an <img> tag for the country's flag, or "" if none.
func countryFlagImg(code string) string {
	url := countryFlagURL(code)
	if url == "" {
		return ""
	}
	return `<img class="vp-flag-img" src="` + url + `" alt="" width="20" height="15" loading="lazy" decoding="async">`
}

// countryDisplayHTML is the HTML form used in server-rendered tables: a real
// self-hosted SVG flag image followed by the HTML-escaped country name. Unlike
// flag emoji (which Windows omits from its system font), the SVG renders
// identically on every platform.
func countryDisplayHTML(code string) string {
	name := html.EscapeString(countryName(code))
	if img := countryFlagImg(code); img != "" {
		return img + ` ` + name
	}
	return name
}

// countryNames maps ISO 3166-1 alpha-2 codes to English short names. Render-only.
var countryNames = map[string]string{
	"AD": "Andorra", "AE": "United Arab Emirates", "AF": "Afghanistan",
	"AG": "Antigua & Barbuda", "AI": "Anguilla", "AL": "Albania",
	"AM": "Armenia", "AO": "Angola", "AQ": "Antarctica", "AR": "Argentina",
	"AS": "American Samoa", "AT": "Austria", "AU": "Australia", "AW": "Aruba",
	"AX": "Åland Islands", "AZ": "Azerbaijan", "BA": "Bosnia & Herzegovina",
	"BB": "Barbados", "BD": "Bangladesh", "BE": "Belgium", "BF": "Burkina Faso",
	"BG": "Bulgaria", "BH": "Bahrain", "BI": "Burundi", "BJ": "Benin",
	"BL": "St. Barthélemy", "BM": "Bermuda", "BN": "Brunei", "BO": "Bolivia",
	"BQ": "Caribbean Netherlands", "BR": "Brazil", "BS": "Bahamas",
	"BT": "Bhutan", "BV": "Bouvet Island", "BW": "Botswana", "BY": "Belarus",
	"BZ": "Belize", "CA": "Canada", "CC": "Cocos (Keeling) Islands",
	"CD": "DR Congo", "CF": "Central African Republic", "CG": "Congo - Brazzaville",
	"CH": "Switzerland", "CI": "Côte d'Ivoire", "CK": "Cook Islands",
	"CL": "Chile", "CM": "Cameroon", "CN": "China", "CO": "Colombia",
	"CR": "Costa Rica", "CU": "Cuba", "CV": "Cape Verde", "CW": "Curaçao",
	"CX": "Christmas Island", "CY": "Cyprus", "CZ": "Czechia", "DE": "Germany",
	"DJ": "Djibouti", "DK": "Denmark", "DM": "Dominica", "DO": "Dominican Republic",
	"DZ": "Algeria", "EC": "Ecuador", "EE": "Estonia", "EG": "Egypt",
	"EH": "Western Sahara", "ER": "Eritrea", "ES": "Spain", "ET": "Ethiopia",
	"FI": "Finland", "FJ": "Fiji", "FK": "Falkland Islands", "FM": "Micronesia",
	"FO": "Faroe Islands", "FR": "France", "GA": "Gabon", "GB": "United Kingdom",
	"GD": "Grenada", "GE": "Georgia", "GF": "French Guiana", "GG": "Guernsey",
	"GH": "Ghana", "GI": "Gibraltar", "GL": "Greenland", "GM": "Gambia",
	"GN": "Guinea", "GP": "Guadeloupe", "GQ": "Equatorial Guinea", "GR": "Greece",
	"GS": "South Georgia & South Sandwich Islands", "GT": "Guatemala",
	"GU": "Guam", "GW": "Guinea-Bissau", "GY": "Guyana", "HK": "Hong Kong",
	"HM": "Heard & McDonald Islands", "HN": "Honduras", "HR": "Croatia",
	"HT": "Haiti", "HU": "Hungary", "ID": "Indonesia", "IE": "Ireland",
	"IL": "Israel", "IM": "Isle of Man", "IN": "India",
	"IO": "British Indian Ocean Territory", "IQ": "Iraq", "IR": "Iran",
	"IS": "Iceland", "IT": "Italy", "JE": "Jersey", "JM": "Jamaica",
	"JO": "Jordan", "JP": "Japan", "KE": "Kenya", "KG": "Kyrgyzstan",
	"KH": "Cambodia", "KI": "Kiribati", "KM": "Comoros", "KN": "St. Kitts & Nevis",
	"KP": "North Korea", "KR": "South Korea", "KW": "Kuwait", "KY": "Cayman Islands",
	"KZ": "Kazakhstan", "LA": "Laos", "LB": "Lebanon", "LC": "St. Lucia",
	"LI": "Liechtenstein", "LK": "Sri Lanka", "LR": "Liberia", "LS": "Lesotho",
	"LT": "Lithuania", "LU": "Luxembourg", "LV": "Latvia", "LY": "Libya",
}

func init() {
	more := map[string]string{
		"MA": "Morocco", "MC": "Monaco", "MD": "Moldova", "ME": "Montenegro",
		"MF": "St. Martin", "MG": "Madagascar", "MH": "Marshall Islands",
		"MK": "North Macedonia", "ML": "Mali", "MM": "Myanmar (Burma)",
		"MN": "Mongolia", "MO": "Macao", "MP": "Northern Mariana Islands",
		"MQ": "Martinique", "MR": "Mauritania", "MS": "Montserrat", "MT": "Malta",
		"MU": "Mauritius", "MV": "Maldives", "MW": "Malawi", "MX": "Mexico",
		"MY": "Malaysia", "MZ": "Mozambique", "NA": "Namibia", "NC": "New Caledonia",
		"NE": "Niger", "NF": "Norfolk Island", "NG": "Nigeria", "NI": "Nicaragua",
		"NL": "Netherlands", "NO": "Norway", "NP": "Nepal", "NR": "Nauru",
		"NU": "Niue", "NZ": "New Zealand", "OM": "Oman", "PA": "Panama",
		"PE": "Peru", "PF": "French Polynesia", "PG": "Papua New Guinea",
		"PH": "Philippines", "PK": "Pakistan", "PL": "Poland",
		"PM": "St. Pierre & Miquelon", "PN": "Pitcairn Islands", "PR": "Puerto Rico",
		"PS": "Palestine", "PT": "Portugal", "PW": "Palau", "PY": "Paraguay",
		"QA": "Qatar", "RE": "Réunion", "RO": "Romania", "RS": "Serbia",
		"RU": "Russia", "RW": "Rwanda", "SA": "Saudi Arabia", "SB": "Solomon Islands",
		"SC": "Seychelles", "SD": "Sudan", "SE": "Sweden", "SG": "Singapore",
		"SH": "St. Helena", "SI": "Slovenia", "SJ": "Svalbard & Jan Mayen",
		"SK": "Slovakia", "SL": "Sierra Leone", "SM": "San Marino", "SN": "Senegal",
		"SO": "Somalia", "SR": "Suriname", "SS": "South Sudan",
		"ST": "São Tomé & Príncipe", "SV": "El Salvador", "SX": "Sint Maarten",
		"SY": "Syria", "SZ": "Eswatini", "TC": "Turks & Caicos Islands",
		"TD": "Chad", "TF": "French Southern Territories", "TG": "Togo",
		"TH": "Thailand", "TJ": "Tajikistan", "TK": "Tokelau", "TL": "Timor-Leste",
		"TM": "Turkmenistan", "TN": "Tunisia", "TO": "Tonga", "TR": "Turkey",
		"TT": "Trinidad & Tobago", "TV": "Tuvalu", "TW": "Taiwan", "TZ": "Tanzania",
		"UA": "Ukraine", "UG": "Uganda", "UM": "U.S. Outlying Islands",
		"US": "United States", "UY": "Uruguay", "UZ": "Uzbekistan",
		"VA": "Vatican City", "VC": "St. Vincent & Grenadines", "VE": "Venezuela",
		"VG": "British Virgin Islands", "VI": "U.S. Virgin Islands", "VN": "Vietnam",
		"VU": "Vanuatu", "WF": "Wallis & Futuna", "WS": "Samoa", "XK": "Kosovo",
		"YE": "Yemen", "YT": "Mayotte", "ZA": "South Africa", "ZM": "Zambia",
		"ZW": "Zimbabwe",
	}
	for k, v := range more {
		countryNames[k] = v
	}
}
