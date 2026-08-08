// SPDX-License-Identifier: Apache-2.0

// Command release-keygen generates the Ed25519 keypair that signs releases.
//
// It exists as a committed tool rather than a command pasted into a chat or a
// wiki, for the same reason the signer does: the thing that mints a signing key
// should be reviewable in the repository that trusts it.
//
// It writes NOTHING to disk. The private half is printed once, to standard
// output, and the operator moves it straight into the release runner's secret
// store. Writing it to a file would leave it in a shell history, a backup, or a
// stray temp directory — none of which anyone remembers to clean.
//
//	go run ./scripts/release-keygen
//
// The two halves go to different places and that separation is the whole point:
//
//	PRIVATE — repository secret VAYU_RELEASE_SIGNING_KEY. Never committed, never
//	          pasted into a chat or an issue, never logged. It is the only thing
//	          an attacker who already controls the release workflow still lacks.
//	PUBLIC  — compiled into the binary as releaseEd25519PubKey. It is not a
//	          secret; it is published deliberately so every install verifies
//	          against the same key.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
)

func main() {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fmt.Fprintln(os.Stderr, "release-keygen: generate:", err)
		os.Exit(1)
	}

	// Printed in the order they are used, each with where it goes, because a bare
	// pair of hex strings is exactly how a private key ends up in the wrong box.
	fmt.Println("PRIVATE KEY — store as repository secret VAYU_RELEASE_SIGNING_KEY")
	fmt.Println("  Settings -> Secrets and variables -> Actions -> New repository secret")
	fmt.Println()
	fmt.Println("   " + hex.EncodeToString(priv))
	fmt.Println()
	fmt.Println("PUBLIC KEY — compile into internal/update/releasekey.go")
	fmt.Println("  const releaseEd25519PubKey = \"" + hex.EncodeToString(pub) + "\"")
	fmt.Println()
	fmt.Println("The private half is shown once and is not written to disk. If it reaches a")
	fmt.Println("chat, an issue, a log or a shell history, treat it as compromised: generate a")
	fmt.Println("new pair, replace the secret, and ship a release carrying the new public key.")
	fmt.Println()
	fmt.Println("Until a release is published carrying the public key above, installs verify")
	fmt.Println("the Sigstore signature only — which is correct, not a gap: there is no")
	fmt.Println("Ed25519 signature in existence for them to check yet.")
}
