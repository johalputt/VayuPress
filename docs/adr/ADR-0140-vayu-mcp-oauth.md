# ADR-0140 — VayuMCP Stage 4: OAuth 2.1 for one-click Connect

Status: Accepted
Date: 2026-07-18
Deciders: VayuPress core
Relates to: ADR-0139 (VayuMCP connector), ADR-0134 (VayuAPI scoped keys)

## Context

VayuMCP (ADR-0139) lets Claude and any MCP client drive a VayuPress site through
`POST /mcp`, authenticated by a scoped API key. Stages 1–3 shipped the connector,
a one-click key-minting page, and a broad tool surface. The last missing piece is
the **true one-click Connect** experience on claude.ai: the user clicks *Connect*,
signs in to their site, approves an access level, and the connection is live — no
key to copy and paste. That is exactly what OAuth 2.1 provides, and it is what the
GitHub connector does.

## Decision

Add a built-in **OAuth 2.1 authorization server** to the VayuPress binary. It is
spec-compliant enough for claude.ai's MCP connector and any OAuth MCP client, and
it reuses the scoped-key model for the actual credential so nothing about *runtime*
authorization changes.

### Key idea: the access token IS a scoped API key

The OAuth server does **not** invent a new token type or a parallel authorization
path. On a successful `/token` exchange it **mints a VayuPress API key**
(`internal/apikeys`, ADR-0134) with the capabilities the operator approved, and
returns that key's token as the OAuth `access_token`. Consequences:

- `/mcp` keeps a single auth path (`requireMCPAuth` → `ResolveValidAPIKey`); an
  OAuth-issued token is just a `vp_` key, so the capability table, per-key rate
  budget, WORM audit, and revocation all apply **verbatim**.
- **No bearer token is ever stored at rest.** The authorization code carries only
  the approved *grant* (a capability list); the key is minted at token-exchange
  time and its token is returned once, exactly like a manually-minted key.
- Revoking the key in **VayuOS → API Keys** instantly disconnects the connector,
  and also drops its refresh token (so it cannot rotate back in).

### Endpoints

| Endpoint | Spec | Auth |
|---|---|---|
| `GET /.well-known/oauth-authorization-server` | RFC 8414 metadata | public |
| `GET /.well-known/oauth-protected-resource` | RFC 9728 metadata | public |
| `POST /oauth/register` | RFC 7591 dynamic client registration | public |
| `GET /oauth/authorize` | consent screen | **operator session (admin)** |
| `POST /oauth/authorize/consent` | approval | operator session + CSRF |
| `POST /oauth/token` | code + refresh exchange | PKCE / refresh token |
| `POST /mcp` (401) | `WWW-Authenticate: Bearer resource_metadata=…` | — |

### Flow

1. An MCP client hits `POST /mcp` with no token → `401` with a `WWW-Authenticate`
   header pointing at the protected-resource metadata (RFC 9728).
2. The client reads the metadata, finds the authorization server, and (RFC 7591)
   **registers itself** at `/oauth/register` with its redirect URIs.
3. The client sends the user's browser to `/oauth/authorize` with PKCE
   (`code_challenge`, `S256`) and `state`.
4. VayuPress validates the client and the **exact** redirect URI *before* any
   redirect (no open redirect), requires a signed-in **administrator** (bouncing
   through `/os/login?next=…` — a strict same-origin return path — if needed), and
   shows a **consent screen**: Full control / Author / Read-only, the same presets
   as the connector page.
5. On approval, VayuPress issues a single-use, short-lived authorization **code**
   bound to the client, redirect URI, PKCE challenge, and the approved grant, and
   redirects back to the client with `code` + `state`.
6. The client calls `/oauth/token` with the code and its PKCE **verifier**.
   VayuPress verifies PKCE (S256, constant-time), mints the scoped key, and returns
   `access_token` (the key) + a `refresh_token`.
7. The client calls `/mcp` with `Authorization: Bearer <access_token>` — a normal
   scoped-key request from there on. Refreshing rotates the underlying key.

### Security properties

- **PKCE S256 mandatory** (OAuth 2.1); the `plain` method is not offered.
- **Exact-match redirect URIs**, constant-time; only `https://` (or
  `http://localhost` for dev) is accepted at registration, fragments rejected.
- **Single-use codes and refresh tokens**, deleted on (even failed) exchange, so a
  code cannot be brute-forced or replayed; codes are hashed at rest and expire in
  5 minutes.
- **Only a signed-in admin can approve**, verified again on the POST; CSRF-guarded
  consent form; the operator owns the minted key and can revoke it.
- **Never redirect to an unvalidated URI** — client/redirect errors render a local
  page; only post-validation protocol errors are reported back via redirect.
- Access tokens inherit **all** ADR-0134 protections; there is no bypass.

## Consequences

- One binary still: the AS is a handful of handlers + one migration (`066`) + the
  host-agnostic `internal/oauth` package (clients, PKCE codes, refresh rotation),
  all unit-tested.
- claude.ai (and any OAuth MCP client) can now Connect with a real sign-in +
  approve button, no key copy-paste — while the API-key connector (ADR-0139)
  keeps working unchanged for clients that prefer it.
- The login page gains a strictly-guarded `next` return path (`safeLocalNext`,
  same-origin only) so the consent flow can bounce through sign-in.

## Alternatives considered

- **A separate OAuth token type + validation path.** Rejected: it would duplicate
  the capability/rate/audit/revocation machinery and create a second thing to get
  right. Reusing scoped keys as access tokens keeps `/mcp` single-path.
- **Storing the minted token on the auth code.** Rejected: it would put a live
  bearer token at rest for the code's lifetime. Minting at `/token` from the stored
  *grant* keeps zero tokens at rest.
- **A third-party OAuth server library.** Deferred: the required surface (metadata,
  DCR, PKCE code flow, refresh) is small and security-sensitive enough to own
  directly, consistent with the rest of VayuPress (minimal deps, small vuln
  surface).
