// SPDX-License-Identifier: Apache-2.0

// Command release-sign produces the Ed25519 signature the updater verifies as
// its second lock, alongside the Sigstore signature.
//
// It lives in scripts/ rather than in the product binary on purpose. Signing is
// something the release runner does once; putting it in cmd/vayupress would add
// a subcommand every install carries, on the very binary whose integrity this
// exists to protect.
//
// It signs the SHA-256 DIGEST rather than the file, which is what
// verifyReleaseEd25519 checks and what lets a forty-megabyte artifact be
// verified without being held twice.
//
// The private key arrives in VAYU_RELEASE_SIGNING_KEY and is never written
// anywhere: not to a file, not to the log, not into the error messages below.
package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
)

func main() {
	if len(os.Args) != 2 {
		fail("usage: release-sign <file>   (writes <file>.sig)")
	}
	keyHex := strings.TrimSpace(os.Getenv("VAYU_RELEASE_SIGNING_KEY"))
	if keyHex == "" {
		// Deliberately fatal. A signing step that quietly does nothing when the
		// secret is missing is how the release pipeline came to publish unsigned
		// artifacts nobody noticed.
		fail("VAYU_RELEASE_SIGNING_KEY is not set — refusing to publish an unsigned artifact")
	}
	key, err := hex.DecodeString(keyHex)
	if err != nil || len(key) != ed25519.PrivateKeySize {
		// Length only; never the value or any part of it.
		fail(fmt.Sprintf("VAYU_RELEASE_SIGNING_KEY is not a hex Ed25519 private key (decoded %d bytes, want %d)",
			len(key), ed25519.PrivateKeySize))
	}
	data, err := os.ReadFile(os.Args[1]) //nolint:gosec // operator-supplied build artifact path
	if err != nil {
		fail("read " + os.Args[1] + ": " + err.Error())
	}
	digest := sha256.Sum256(data)
	sig := ed25519.Sign(ed25519.PrivateKey(key), digest[:])

	out := os.Args[1] + ".sig"
	if err := os.WriteFile(out, []byte(hex.EncodeToString(sig)), 0o644); err != nil { //nolint:gosec // a public signature
		fail("write " + out + ": " + err.Error())
	}
	// The public half is printed so a release log records which key signed, and an
	// operator can confirm it matches the one compiled into the binary.
	pub := ed25519.PrivateKey(key).Public().(ed25519.PublicKey)
	fmt.Printf("signed %s -> %s (public key %s)\n", os.Args[1], out, hex.EncodeToString(pub))
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "release-sign: "+msg)
	os.Exit(1)
}
