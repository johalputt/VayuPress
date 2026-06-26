# ADR-0084: VayuMail Outbound Deliverability — Vetted DKIM Signing + Well-Formed MIME

**Status**: Accepted (amends ADR-0079)
**Date**: 2026-06-25
**Author**: @johalputt

## Context

VayuMail signs outbound mail with DKIM and delivers it direct-to-MX. In
practice, legitimate messages were still being filed to **spam by Gmail**.
Outbound inbox placement depends on several factors, only some of which are
solvable in code:

- **Infrastructure / DNS (operator-owned):** sending-IP reputation, reverse DNS
  (PTR) matching the mail host, and correctly published SPF / DKIM / DMARC
  records. A brand-new self-hosted IP has no sending history and is treated with
  suspicion regardless of configuration.
- **Message correctness (code-owned):** a cryptographically valid DKIM
  signature and a well-formed MIME message.

ADR-0079 deliberately kept outbound DKIM **signing** as an in-house
implementation while adopting vetted libraries only for inbound verification. A
subtle bug in hand-rolled canonicalization, however, is one of the most common
reasons a message that "looks" signed still fails `dkim=` verification at the
receiver — and under an enforcing DMARC policy (`p=quarantine`) that failure
sends the message straight to spam. This is exactly the class of risk the
project already chose to avoid on the inbound path.

## Decision

1. **Sign outbound mail with the vetted library.** Outbound DKIM signing now
   uses `github.com/emersion/go-msgauth/dkim` (already a dependency for inbound
   verification) instead of the bespoke canonicalizer. Parameters are unchanged
   in substance: relaxed/relaxed canonicalization, `rsa-sha256`, RSA-2048 key,
   selector `vayu`, `d=` aligned to the configured domain. The whole assembled
   message is signed as one unit, so the signed bytes are exactly the bytes
   transmitted. This supersedes the "outbound DKIM signing remains in-house"
   note in ADR-0079 §3.

2. **Emit well-formed MIME.** A message carrying both a text and an HTML body is
   sent as `multipart/alternative` (text part first, HTML second) with explicit
   `Content-Transfer-Encoding` and canonical CRLF line endings — the structure
   mainstream clients send and that spam classifiers expect. Mandatory headers
   (`Date`, `Message-ID`, `MIME-Version`) are always present. The inline PGP
   path is unchanged: a single ASCII-armored `text/plain` part, never paired
   with an HTML alternative.

3. **Self-check the operator-owned factors.** The deliverability panel already
   verifies the published DKIM key vs the signing key and reverse DNS (PTR); it
   now also flags a mail hostname that is **not a fully-qualified domain name**
   (a "localhost"/bare-label EHLO is an immediate spam signal).

## Consequences

- Positive: eliminates the bespoke-canonicalization failure mode; messages are
  structurally indistinguishable from those of mainstream clients. No new
  dependency — the library was already vendored for inbound checks.
- **Honest limitation:** no code change can *guarantee* Gmail inbox placement
  for a fresh self-hosted IP. Sending-IP reputation and correct DNS (PTR + SPF +
  DKIM + DMARC) dominate, and a new IP typically needs a warm-up period. The
  self-check surfaces the operator-owned items; verifying with a tool such as
  mail-tester and enrolling in Google Postmaster Tools is recommended.
- Trade-off: none of note — the change removes hand-rolled crypto in favour of a
  vetted implementation already trusted elsewhere in the codebase.
