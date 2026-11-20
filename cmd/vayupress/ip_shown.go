// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net"
	"regexp"

	"github.com/johalputt/vayupress/internal/signin"
)

// ipCandidate is anything shaped like an IPv4 or IPv6 address; net.ParseIP
// decides whether it is one.
var ipCandidate = regexp.MustCompile(`[0-9A-Fa-f:.]{3,}[0-9A-Fa-f]`)

// ipsMasked is text with every IP address in it shown as Sign-in attempts
// shows one (signin.Mask: 223.184.x.x). A record that keeps the whole address,
// such as the append-only audit log, is masked where it is shown, not where
// it is written, so the record itself stays complete.
func ipsMasked(text string) string {
	return ipCandidate.ReplaceAllStringFunc(text, func(m string) string {
		if ip := net.ParseIP(m); ip != nil {
			return signin.Mask(ip)
		}
		return m
	})
}
