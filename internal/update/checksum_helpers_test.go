// SPDX-License-Identifier: Apache-2.0

package update

import "testing"

func TestChecksumForFile(t *testing.T) {
	const h = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const h2 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb0"
	// Single-line per-binary .sha256 ("<hex>  <path>").
	if got := checksumForFile([]byte(h+"  dist/vayupress\n"), "vayupress"); got != h {
		t.Errorf("single-line: got %q", got)
	}
	// Combined SHA256SUMS with several files → pick the binary's line.
	multi := h2 + "  vayupress.sig\n" + h + " *vayupress\n" + "cccc  other\n"
	if got := checksumForFile([]byte(multi), "vayupress"); got != h {
		t.Errorf("multi-line: got %q, want %q", got, h)
	}
}

func TestBinaryDownloadProblem(t *testing.T) {
	if binaryDownloadProblem([]byte("\x7fELF\x02\x01\x01anything")) != "" {
		t.Error("a real ELF binary must not be flagged")
	}
	if binaryDownloadProblem(nil) == "" {
		t.Error("empty download must be flagged")
	}
	if binaryDownloadProblem([]byte("  <!DOCTYPE html><html>...</html>")) == "" {
		t.Error("HTML error page must be flagged")
	}
	if binaryDownloadProblem([]byte(`{"message":"Not Found"}`)) == "" {
		t.Error("JSON error page must be flagged")
	}
}
