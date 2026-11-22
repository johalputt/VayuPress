// SPDX-License-Identifier: Apache-2.0

// Package pwned asks whether a password is in Have I Been Pwned's corpus of
// breached passwords without the password, or its hash, leaving the server.
//
// The range API is k-anonymous: the request names the first five hex
// characters of the password's SHA-1, which some hundreds of breached
// hashes share, and the answer lists the rest of each with its count; the
// match is made here. SHA-1 is the API's choice, used as an index into a
// public list, not to protect anything.
package pwned

import (
	"crypto/sha1" //nolint:gosec // the range API indexes by SHA-1; nothing is protected by it
	"encoding/hex"
	"strconv"
	"strings"
)

// RangeURL is where the five-character prefix is asked about.
const RangeURL = "https://api.pwnedpasswords.com/range/"

// Split is the prefix the request names and the suffix looked for in the
// answer.
func Split(password string) (prefix, suffix string) {
	sum := sha1.Sum([]byte(password)) //nolint:gosec // see the package comment
	h := strings.ToUpper(hex.EncodeToString(sum[:]))
	return h[:5], h[5:]
}

// Found reports whether suffix is listed in a range answer with a count
// above nought ("SUFFIX:COUNT" per line; a padding line counts nought).
func Found(answer []byte, suffix string) bool {
	for _, line := range strings.Split(string(answer), "\n") {
		s, n, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || !strings.EqualFold(s, suffix) {
			continue
		}
		if c, err := strconv.Atoi(strings.TrimSpace(n)); err == nil && c > 0 {
			return true
		}
	}
	return false
}
