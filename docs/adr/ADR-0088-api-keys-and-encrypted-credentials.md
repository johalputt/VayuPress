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
     plaintext at runtime. They are sealed with **AES-256-GCM**.
2. **Envelope encryption decoupled from any auth credential (100% automated
   rotation).** Credentials are not encrypted directly with the API key.
   Instead a persistent, randomly-generated **Data Encryption Key (DEK)**,
   stored once in the `secret_keyring` table, encrypts every credential. The DEK
   is independent of the API key, so rotating any key — including the bootstrap
   `API_KEY` — never makes a stored secret undecryptable and never requires
   re-entering anything.
   - By default the DEK is self-managed (stored directly; the database file is
     the trust boundary on a single VPS).
   - If `VAYU_SECRET` is set, the DEK is **wrapped** by a Key Encryption Key
     (KEK) derived from it (domain-separated SHA-256) for defence-in-depth, with
     a sealed check value to detect a wrong/missing secret at boot.
   - Because only the small DEK is wrapped, `RewrapMaster` can introduce or
     change the encryption secret **in place** without re-encrypting — or
     losing — a single credential.

   This intentionally supersedes the earlier reflex of reusing the `API_KEY`
   master (as VayuPGP does, ADR-0076): for *recoverable* secrets we want
   rotation of the auth key to be consequence-free.
3. **Auth integration via a registered verifier.** `internal/auth` exposes
   `SetExtraAPIKeyVerifier`; `RequireAPIKey` and `HasValidAPIKey` accept a
   DB-issued key as a fallback to the static `API_KEY`, keeping the existing
   constant-time comparison and IP-lockout protections. The env key remains a
   valid bootstrap credential.
4. **Internal vs external key scopes.** `vayu_api_keys.scope` separates an
   auto-provisioned **internal/system** key (`scope='internal'`) from
   operator-issued **external** keys. The system key is created automatically at
   boot, exposed to internal automation via `App.InternalAPIKey()` (read live,
   so a rotation propagates with no manual step), and is protected from
   revoke/delete.
5. **IndexNow becomes self-serving.** `pingIndexNow` resolves its key from the
   secrets store first, then the env var. The verification file at
   `/.well-known/<key>.txt` is served dynamically whenever a key is configured,
   so no manual upload is required.
6. **First-class provider cards** for IndexNow, OpenRouter, Ollama and n8n, plus
   a generic "custom" credential, all on a new VayuOS **API Keys** page.

## Consequences

- Positive: runtime key issuance/rotation/revocation; **rotating any key never
  affects stored third-party secrets** (no manual re-entry); the internal key is
  zero-config and self-propagating; integration secrets are encrypted at rest
  and never displayed in clear after saving; IndexNow works end-to-end from the
  UI.
- Trade-off: with `VAYU_SECRET` set, that secret becomes the stable unlock for
  the keyring — changing/losing it out-of-band without `RewrapMaster` would make
  the DEK (and thus the credentials) unreadable. That is why `VAYU_SECRET` is a
  dedicated, stable encryption secret, explicitly *not* the rotatable `API_KEY`.
  With no `VAYU_SECRET`, the DEK lives in the database, whose file is the trust
  boundary on a single-VPS deployment.
- Migrations: `041-api-keys` adds `vayu_api_keys` and `service_credentials`;
  `042-api-keys-envelope` adds `secret_keyring` and the `scope` column.
