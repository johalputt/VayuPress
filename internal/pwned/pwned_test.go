// SPDX-License-Identifier: Apache-2.0

package pwned

import "testing"

// "password" is SHA-1 5BAA61E4C9B93F3F0682250B6CF8331B7EE68FD8: the request
// names 5BAA6 and nothing else.
func TestThePrefixIsAllThatIsAsked(t *testing.T) {
	prefix, suffix := Split("password")
	if prefix != "5BAA6" || suffix != "1E4C9B93F3F0682250B6CF8331B7EE68FD8" {
		t.Fatalf("split as %q and %q", prefix, suffix)
	}
}

// A suffix listed with a count is found, in either case; one listed with a
// count of nought (padding) or not at all is not.
func TestASuffixIsFoundOnlyWithACount(t *testing.T) {
	answer := []byte("0018A45C4D1DEF81644B54AB7F969B88D65:1\r\n1E4C9B93F3F0682250B6CF8331B7EE68FD8:52256179\r\nFFFFF0000000000000000000000000000000:0\r\n")
	if !Found(answer, "1e4c9b93f3f0682250b6cf8331b7ee68fd8") {
		t.Error("a listed suffix was not found")
	}
	if Found(answer, "FFFFF0000000000000000000000000000000") {
		t.Error("a padding line was counted as a breach")
	}
	if Found(answer, "ABCDEF0000000000000000000000000000000") {
		t.Error("an unlisted suffix was found")
	}
}
