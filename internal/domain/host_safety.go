// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"fmt"
	"strings"
)

// Hostname limits from RFC 1035 §2.3.4, which are also the limits every
// filesystem and nginx directive downstream of this can carry.
const (
	maxHostLen  = 253
	maxLabelLen = 63
)

// ValidateHost reports whether host is safe to hand to the root provisioning
// worker, which interpolates it into an nginx directive and into the name of a
// file it writes as root:
//
//	scripts/setup-vayudomain.sh:251  cat > "${AVAIL_DIR}/vayupress-dom-$1" <<NGINX
//	scripts/setup-vayudomain.sh:254      server_name $1 www.$1;
//
// Deliberately an allowlist of letters, digits, hyphen and dot — the LDH rule.
// A denylist of "characters that break nginx" would be a guess at a parser this
// project does not own, and would have to be right about `;`, `{`, `}`, `#`,
// newline, quotes and whatever the next nginx version adds. Punycode is ASCII
// (`xn--…`), so internationalised domains pass unchanged. Upper case is accepted
// because it is harmless to interpolate; canonical lowercasing is NormalizeHost's
// job, and conflating the two would let a casing slip look like an attack.
//
// Kept apart from NormalizeHost on purpose. That function also runs on the
// REQUEST path, resolving a Host header to a registry entry on every hit;
// rejecting there would turn a malformed header into a serving failure rather
// than a miss, and would put this cost on the hot path. This runs where a
// hostname is stored and where one is handed to root, which is a handful of
// calls, not one per request.
func ValidateHost(host string) error {
	switch {
	case host == "":
		return fmt.Errorf("domain: host is required")
	case len(host) > maxHostLen:
		return fmt.Errorf("domain: host is %d bytes; the limit is %d", len(host), maxHostLen)
	case strings.HasPrefix(host, "."), strings.HasSuffix(host, "."):
		return fmt.Errorf("domain: host %q begins or ends with a dot", host)
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" {
			return fmt.Errorf("domain: host %q has an empty label", host)
		}
		if len(label) > maxLabelLen {
			return fmt.Errorf("domain: label %q is %d bytes; the limit is %d", label, len(label), maxLabelLen)
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("domain: label %q begins or ends with a hyphen", label)
		}
		for _, r := range label {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			default:
				// Named rather than summarised: an operator who typed a stray
				// character needs to know which one, and a report of "invalid
				// hostname" sends them to re-read the whole string.
				return fmt.Errorf("domain: host %q contains %q, which is not allowed in a hostname", host, r)
			}
		}
	}
	return nil
}
