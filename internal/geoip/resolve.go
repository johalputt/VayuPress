// SPDX-License-Identifier: Apache-2.0

package geoip

import "strings"

// Source names where a country verdict came from. The panel reports it, because
// "which country is this visitor in" turned out to have two answers on a live
// install and no way to see which one any given control had used.
const (
	SourceNone  = ""      // nothing could answer
	SourceEdge  = "edge"  // a trusted CDN said so, per request
	SourceTable = "table" // the embedded offline table, from the address
)

// Resolve returns the country for a request and the source that answered.
//
// THE DEFECT THIS EXISTS TO END. A live install refused Singapore, and Analytics
// went on reporting Singapore as 91% of its audience while the shield's own trail
// held not one request from there in seven days. Neither side was broken. They
// were reading DIFFERENT SOURCES: Analytics took CF-IPCountry, which the edge
// computes per request from its own live data; the shield looked the address up
// in a table compiled into the binary at release time. Two answers, one request,
// nothing anywhere able to show they disagreed — so an operator writing a rule
// against the country they could SEE was writing it against a country the
// enforcement path never used.
//
// The edge wins when it has spoken, for two reasons that point the same way: it
// is the fresher data, and — decisively — it is the number the operator is
// looking at when they type a country into a rule. A control must act on the
// fact its operator saw.
//
// header must already have been taken from a TRUSTED peer. This function cannot
// check that and does not try; the caller owns it, exactly as auth.ClientIP owns
// the same question for the address. Passing an untrusted header here lets a
// visitor choose their own country.
func Resolve(header, ip string) (country, source string) {
	if c := normaliseCountry(header); c != "" {
		return c, SourceEdge
	}
	if c := normaliseCountry(Country(ip)); c != "" {
		return c, SourceTable
	}
	return "", SourceNone
}

// normaliseCountry upper-cases a code and drops the values that are not one.
//
// "XX" and "T1" are Cloudflare's placeholders for unknown and Tor. They are not
// countries, and an operator cannot write a rule that means them — treating them
// as codes would let "deny XX" look like a control while matching a bucket the
// edge uses for "I don't know".
func normaliseCountry(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	if len(s) != 2 || s == "XX" || s == "T1" {
		return ""
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 'A' || s[i] > 'Z' {
			return ""
		}
	}
	return s
}
