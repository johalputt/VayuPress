// SPDX-License-Identifier: Apache-2.0

package theme

import "encoding/json"

// SiteHalcyonOptions are the Halcyon options a hosted site may choose for
// itself: all but the accent, since a site already sets its own colours.
func SiteHalcyonOptions() []Option {
	out := make([]Option, 0, len(halcyonOptions))
	for _, o := range halcyonOptions {
		if o.Key != "accent" {
			out = append(out, o)
		}
	}
	return out
}

// ParseSiteHalcyon reads a site's stored options (a JSON object of option key
// to choice). ok is false for anything else: an unknown key, a value that is
// not one of its option's choices, or not an object. Blank is valid and
// means the site has chosen nothing.
func ParseSiteHalcyon(raw string) (opts map[string]string, ok bool) {
	if raw == "" {
		return nil, true
	}
	if err := json.Unmarshal([]byte(raw), &opts); err != nil || opts == nil {
		return nil, false
	}
	for k, v := range opts {
		valid := false
		for _, o := range SiteHalcyonOptions() {
			if o.Key != k {
				continue
			}
			for _, c := range o.Choices {
				valid = valid || c.Value == v
			}
		}
		if !valid {
			return nil, false
		}
	}
	return opts, true
}

// SiteHalcyon is a site's own Halcyon config from its stored options, or nil
// when it has chosen none (or what is stored does not parse), so the
// install's applies.
func SiteHalcyon(raw string) *HalcyonConfig {
	opts, ok := ParseSiteHalcyon(raw)
	if !ok || len(opts) == 0 {
		return nil
	}
	cfg := ResolveHalcyon(opts)
	return &cfg
}
