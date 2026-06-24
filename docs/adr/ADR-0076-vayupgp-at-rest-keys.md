# ADR-0076: VayuPGP — Native PGP With Keys Encrypted At Rest

**Status**: Accepted  
**Date**: 2026-06-24  
**Author**: @johalputt

## Context

v1.8.0 adds VayuPGP, a native end-to-end encryption layer. We need a PGP
implementation that is pure-Go (no CGO), Apache-2.0-compatible, and stores
private keys safely on a single-VPS deployment.

## Decision

1. Build on **ProtonMail go-crypto** (Apache-2.0); its transitive Cloudflare
   CIRCL backend is BSD-3-Clause. No GPL/AGPL is introduced.
2. Generate **Ed25519** primary (sign/certify) + **Curve25519/X25519** subkey
   (encrypt), 2-year expiry.
3. Private keys are serialized armored, then sealed with **AES-256-GCM** using a
   key derived (domain-separated SHA-256) from the VayuPress master secret
   (`API_KEY`). Plaintext private keys never touch disk and are never logged —
   logs record fingerprints only.
4. Key rotation **archives** the previous private key so historical ciphertext
   stays decryptable.
5. Public keys are published via **WKD** at `/.well-known/openpgpkey/`.

## Consequences

- Positive: encryption by architecture; compromise of the on-disk keystore alone
  does not expose private keys without the master secret.
- Trade-off: the master secret (`API_KEY`) must remain stable; rotating it
  requires re-encrypting the keystore (documented in UPGRADING).
