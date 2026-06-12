# ADR-0036: CSP Nonce Centralized Template Helpers

**Status**: Accepted  
**Date**: 2026-06-12  
**Author**: @johalputt

## Context

Content Security Policy (CSP) is only effective against XSS if inline scripts are disallowed via `script-src 'self'`. However, many templates require legitimate inline scripts (e.g., initial page state, critical CSS). Without a nonce mechanism, developers must either disable CSP's script-src restriction (negating its value) or use `'unsafe-inline'` (equally bad).

## Decision

1. Per HTTP request, generate a cryptographically random nonce: `nonce := base64.StdEncoding.EncodeToString(randomBytes(16))`.
2. Inject the nonce into the Go template context via `templateData.CSPNonce`.
3. A centralized helper `cspHeader(nonce string) string` assembles the full CSP header value including `script-src 'nonce-{nonce}'`.
4. The nonce is set on every `<script>` tag in templates as `nonce="{{.CSPNonce}}"`.
5. The CSP header is set via Nginx `add_header` for static assets, and via the Go handler for dynamic responses.

## Rationale

A per-request nonce makes each page's inline scripts unpredictable to an attacker who cannot observe the nonce. Without centralized helpers, developers write the CSP header in multiple places — creating drift between handler CSP values and template nonce usage.

## Consequences

- Positive: XSS attacks cannot inject executable scripts without a valid nonce.
- Positive: Single point of change for CSP policy evolution.
- Negative: Templates must always include `nonce="{{.CSPNonce}}"` on all inline scripts — missed nonces cause silent breakage (script won't run).
- Negative: CDN caching of dynamic pages becomes harder (nonce must vary per response).
