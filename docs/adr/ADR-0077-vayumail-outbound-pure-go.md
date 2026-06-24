# ADR-0077: VayuMail Outbound — Pure-Go DKIM + Direct-MX (No Mox Fork)

**Status**: Accepted  
**Date**: 2026-06-24  
**Author**: @johalputt

## Context

The original Phase 2 plan proposed forking the Mox mail server into the tree.
A full Mox fork is tens of thousands of lines, adds significant binary/attack
surface, and would have to be tracked against upstream — at odds with the
single-binary, low-maintenance constitution.

## Decision

Implement the **outbound** sovereignty path in pure Go on the standard library
rather than vendoring Mox:

1. **DKIM** signing per RFC 6376 (relaxed/relaxed, RSA-2048/SHA-256), key
   generated and persisted under the storage path.
2. **Direct-to-MX** delivery (`net/smtp`) with opportunistic STARTTLS — no
   third-party relay.
3. A **durable SQLite-backed retry queue** with exponential backoff.
4. **Maildir** storage; automatic MX/SPF/DKIM/DMARC record generation with live
   DNS health checks.
5. Outgoing mail is auto-PGP-encrypted when a recipient key is discoverable.

The NOTICE file is corrected: VayuPress does **not** bundle or fork Mox.

## Consequences

- Positive: small, auditable, no upstream-fork maintenance burden; clean
  licence chain.
- Trade-off: we own the protocol code; inbound is delivered separately
  (ADR-0078) rather than inheriting Mox's receive stack.
