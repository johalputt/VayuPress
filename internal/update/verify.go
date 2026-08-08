// SPDX-License-Identifier: Apache-2.0

package update

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
)

// VerifyChecksum computes the SHA-256 of data and compares it (constant-time)
// against the hex-encoded expected digest.
func VerifyChecksum(data []byte, expectedHex string) error {
	expected, err := hex.DecodeString(expectedHex)
	if err != nil {
		return fmt.Errorf("update: decode checksum: %w", err)
	}
	if len(expected) != sha256.Size {
		return fmt.Errorf("update: checksum length %d != %d", len(expected), sha256.Size)
	}
	sum := sha256.Sum256(data)
	if subtle.ConstantTimeCompare(sum[:], expected) != 1 {
		return errors.New("update: checksum mismatch")
	}
	return nil
}
