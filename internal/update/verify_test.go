// SPDX-License-Identifier: Apache-2.0

package update

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestVerifyChecksum(t *testing.T) {
	data := []byte("hello vayupress")
	sum := sha256.Sum256(data)
	if err := VerifyChecksum(data, hex.EncodeToString(sum[:])); err != nil {
		t.Fatalf("expected pass: %v", err)
	}
	if err := VerifyChecksum(data, hex.EncodeToString(sum[:])[:62]+"00"); err == nil {
		t.Error("expected mismatch failure")
	}
	if err := VerifyChecksum(data, "not-hex"); err == nil {
		t.Error("expected decode failure")
	}
}
