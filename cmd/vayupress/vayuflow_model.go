// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/johalputt/vayupress/internal/aiassist"
)

// flowModel adapts the install's configured assistant for VayuFlow.
//
// It uses the env-configured Client — the default local assist — rather than
// the editor's runtime provider picker. That is a deliberate narrowing: the
// picker resolves a provider from a per-request credential lookup, and an
// unattended automation resolving credentials on someone's behalf is a
// different authority question from a human choosing one in the editor. When a
// flow needs a hosted provider, that decision gets its own surface and its own
// entry in the capability registry.
type flowModel struct{ c *aiassist.Client }

// Generate runs one operation through the configured assistant.
func (m flowModel) Generate(ctx context.Context, op, text string) (string, error) {
	if m.c == nil || !m.c.Enabled() {
		return "", fmt.Errorf("the assistant is not configured on this install")
	}
	return m.c.Assist(ctx, op, text)
}

// Local reports whether the provider runs on this host.
//
// The env-configured client is the local Ollama path, so this is true. It is a
// method rather than a constant because the answer decides whether a model step
// is an outbound call — and therefore whether it is refused in a Tor Space and
// charged against the egress ceiling. A wrong answer here would either leak
// from a Tor Space or refuse a local model that was never a risk, so it reads
// the endpoint rather than assuming.
func (m flowModel) Local() bool {
	if m.c == nil {
		return false
	}
	return isLoopbackEndpoint(m.c.Endpoint())
}

// isLoopbackEndpoint reports whether a provider base URL points at this host.
//
// It PARSES the URL and inspects the host. A string-prefix test was the first
// implementation and it was wrong in a way that matters: "http://localhost" is
// a prefix of "http://localhost.evil.example", so an endpoint on an
// attacker-chosen domain read as local — meaning it would NOT be refused in a
// Tor Space and NOT charged against the egress ceiling. A clearnet leak
// reachable by setting a configuration value.
//
// Fails CLOSED: anything it cannot confidently resolve to a loopback host is
// treated as remote, because the costs are not symmetric. Guessing "remote"
// wrongly refuses a local model; guessing "local" wrongly leaks.
func isLoopbackEndpoint(endpoint string) bool {
	raw := strings.TrimSpace(endpoint)
	if raw == "" {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	// Only http(s). A scheme this code does not understand is not a scheme it
	// can make a safety claim about.
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return false
	}
	host := u.Hostname()
	if host == "" {
		return false
	}
	// An IP literal is authoritative: 127.0.0.0/8 and ::1 are loopback, and
	// nothing else is, whatever it is named.
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	// Exactly "localhost" — not a domain that merely ends in it, and not one
	// that merely starts with it.
	return strings.EqualFold(host, "localhost")
}
