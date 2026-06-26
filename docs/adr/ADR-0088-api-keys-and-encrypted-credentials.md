# ADR-0088: API Key Console & Encrypted Third-Party Credentials

**Status**: Accepted  
**Date**: 2026-06-26  
**Author**: @johalputt

## Context

Until v1.17.0 VayuPress authenticated its API with a single static key set via
the `API_KEY` environment variable, and the secrets it needed to call external
services (IndexNow, AI runtimes, automation webhooks) were all env-only. This
had two gaps:

1. **No runtime key management.** Rotating the API key meant editing the
   environment and restarting; there was no way to issue per-integration keys or
   revoke a leaked one without disrupting every caller.
2. **No place to store integration secrets.** IndexNow in particular shipped the
   submission logic but had no UI to set its key, and the ownership-verification
   file it requires was never served — so the feature could not actually be
   enabled from the admin panel.

We want an operator to manage all of this from VayuOS, with secrets protected at
rest on a single-VPS deployment.

## Decision

1. **Two stores, two threat models.**
   - `internal/apikeys` — VayuPress's own bearer tokens. Tokens are
     `vp_<64 hex>`; only a **SHA-256 hash** is persisted (mirroring login
     sessions). The raw token is returned exactly once on create/rotate.
     Verification is an O(1) lookup against an in-memory set of active hashes
     (30 s TTL, invalidated on mutation).
   - `internal/secrets` — third-party credentials that must be recoverable in
     plaintext at runtime. They are sealed with **AES-256-GCM** under a key
     derived (domain-separated SHA-256) from the master secret (`API_KEY`) — the
     same at-rest scheme as VayuPGP (ADR-0076). Only the ciphertext and a masked
     hint are stored.
2. **Auth integration via a registered verifier.** `internal/auth` exposes
   `SetExtraAPIKeyVerifier`; `RequireAPIKey` and `HasValidAPIKey` accept a
   DB-issued key as a fallback to the static `API_KEY`, keeping the existing
   constant-time comparison and IP-lockout protections. The env key remains a
   valid bootstrap credential.
3. **IndexNow becomes self-serving.** `pingIndexNow` resolves its key from the
   secrets store first, then the env var. The verification file at
   `/.well-known/<key>.txt` is served dynamically whenever a key is configured,
   so no manual upload is required.
4. **First-class provider cards** for IndexNow, OpenRouter, Ollama and n8n, plus
   a generic "custom" credential, all on a new VayuOS **API Keys** page.

## Consequences

- Positive: runtime key issuance/rotation/revocation; integration secrets are
  encrypted at rest and never displayed in clear after saving; IndexNow works
  end-to-end from the UI.
- Trade-off: the master secret (`API_KEY`) must remain stable — rotating it
  re-keys the at-rest seal and makes stored third-party secrets undecryptable
  (they must be re-entered). This matches the existing VayuPGP constraint and is
  documented alongside it.
- Migration `041-api-keys` adds `vayu_api_keys` and `service_credentials`.
