---
path: /vayuweb.html
title: VayuWeb — a parallel web, owned by nobody
kicker: VayuWeb
description: A peer-to-peer naming and hosting protocol. Register a name, publish a site, and reach it from an ordinary browser, with no registrar, certificate authority, host or single government in the path.
---
# VayuWeb

Decentralised · Parallel · Web

Coming soon — charter first, code next

A peer-to-peer naming and hosting protocol. Register a name, publish a site, and reach it from an ordinary browser — with no registrar, no certificate authority, no hosting company and no single government in the path.

[Read the Constitution](https://github.com/johalputt/VayuWeb/blob/main/constitution/CONSTITUTION.md) · [Explore the repository](https://github.com/johalputt/VayuWeb)

No token · No treasury · No root zone · MIT

## Two chokepoints decide whether anything on the web stays reachable.

Who resolves your name, and who serves your bytes. Both have consolidated into a handful of companies operating under a handful of jurisdictions — and in practice, the same few organisations hold every lever at once.

- **Registrar** can suspend the domain
- **Resolver** can refuse to answer
- **Authority** can revoke the certificate
- **Host** can deplatform the account
- **Network** can decide you are not worth the trouble

VayuWeb answers by removing the levers rather than negotiating with whoever holds them. Names live in a signed, append-only log that every participant replicates and verifies independently. Content lives on IPFS, pinned by the people who care whether it survives. Resolution happens on your own machine. There is no root zone, no privileged writer, no treasury and no token.

---

Plural by design: 1,270 extensions at launch, of which the constitution names eleven so the founding set survives the loss of its annex (Article 35.1): `.vayu`, `.p2p`, `.free`, `.decent`, `.libre`, `.sov`, `.dao`, `.indie`, `.open`, `.news` and `.blog`.

## What VayuWeb can do that the web cannot.

Nine capabilities, each of which exists because a specific power was removed from the system rather than entrusted to someone. What follows every card is not a feature — it is the thing that becomes structurally impossible.

### Names nobody can take

Ownership is an Ed25519 keypair. There is no registrar account, no billing relationship, no support desk and no override. A name moves when its holder signs, and in no other circumstance.

*Impossible: suspension by anyone.*

### Content verified byte for byte

Every page is addressed by the hash of its own contents and checked before it reaches the browser. Not "the server said it was fine" — the bytes either match the hash or they are refused.

*Impossible: silent tampering in transit.*

### Zero-trail Private Mode

All egress through an anonymising transport, every byte of state in memory, nothing written to disk, and a hard refusal to start rather than fall back if the transport is unavailable.

*Impossible: a silent fallback to clearnet.*

### Its own scheme

Addresses read `vayu://example.vayu/`. Not `https`, because that promises a certificate authority VayuWeb does not use, and a security indicator that lies is worse than none.

*Impossible: mistaking VayuWeb for the clearnet.*

### No token, no treasury

Registration costs seconds of CPU, not money. There is no pot to fight over, embezzle, freeze, tax or subpoena — and no speculator whose interest is the price rather than the protocol.

*Impossible: buying a governance outcome.*

### Post-quantum ready

No primitive is named in the protocol — only versioned suites. Suites move forward only, verifiers keep every historical suite forever, and migration runs through a hybrid so it stays safe even if the target is later broken.

*Impossible: being stranded on a broken cipher.*

### Governed by a written charter

Sixty Articles across six Titles, with an entrenched core no amendment can reach, an Objection Register that records the dissent alongside the decision, and duties that bind the text rather than any body.

*Impossible: an unrecorded decision.*

### Every namespace equal

No extension is the real one. Plurality is the design: a single namespace is a single thing worth capturing, and retiring one requires a two-year sunset with mandatory aliases so nobody is stranded.

*Impossible: cornering the namespace.*

### The right to leave

Fork the protocol, the registry state, or the name itself — and be recognised in the fork without the incumbent network's cooperation. The charter is public domain precisely so a fork can carry it away intact.

*Impossible: a captured system you cannot exit.*

- **0** third-party requests
- **0** query logs, ever
- **60** constitutional articles
- **1,270** extensions at launch, all equal

---

## Deny everything, then allow by name.

Every VayuWeb page is served under a policy that starts from `'none'` and enumerates each exception. A directive nobody remembered to add inherits a denial, not a permission.

The response headers it sends:

```
Content-Security-Policy:
  default-src   'none'
  script-src    'self'
  style-src     'self'
  img-src       'self'
  connect-src   'self'
  worker-src    'none'
  frame-src     'none'
  object-src    'none'
  base-uri      'none'
  webrtc        'block'
  require-trusted-types-for 'script'

Permissions-Policy:
  every named feature = ()

Referrer-Policy:   no-referrer
Cache-Control:     no-store
X-DNS-Prefetch-Control: off

# no report-uri. a reporting endpoint is an
# outbound channel that fires exactly when
# something unexpected happens.
```

### No inline anything

No inline script, no inline style, no `data:` images. Inline CSS is a working exfiltration channel — an attribute selector plus a `url()` leaks a document one character at a time.

### The privileged surface is unreachable

The control API listens on a Unix socket, not a port. A browser cannot address one by any means — which deletes DNS rebinding, CSRF and port-scanning of the privileged surface in a single decision rather than three defences.

### Entropy only matters if it can leave

A browser fingerprint cannot be erased, so VayuWeb starves the channel instead. Perfect identification is worth nothing if it has nowhere to be delivered — which is why none of these refusals is configurable.

The specification also names the four channels no header closes — WebRTC, top-level navigation, timing side channels and a compromised device — and says exactly what closes each instead. A security document that lists only its wins is a marketing document.

## Four layers, two surfaces, nothing in the middle.

| Layer | Technology | Purpose |
| --- | --- | --- |
| Identity | Ed25519 | Ownership and signatures |
| Registry | Hypercore + Hyperbee | Signed, append-only, replicated name records |
| Discovery | Hyperswarm / HyperDHT | Finding other peers |
| Content | IPFS (Helia) + IPNS | Immutable storage and mutable pointers |
| Resolution | Local proxy (loopback) | Makes VayuWeb names work in an ordinary browser |
| Client | Tauri 2.x | Desktop application for people who do not use terminals |
| Governance | Constitution + VWIP | How the rules change, and what can never change |

## Honest status, not a progress bar.

### Charter: complete

Sixty Articles across six Titles, ratified and in force. Public domain.

### Specifications: in review

Registry, naming, resolution, hosting, proof-of-work, scheme, security, privacy, crypto agility. Open to attack.

### Code: not started

No registry, no proxy, no client. Deliberately last — see the roadmap for what each phase must prove.

### The most useful thing you can do right now is attack it

Not write code — there is nothing to write against yet, and code written before the specification settles gets thrown away. Find the clause a determined bad actor could drive a truck through. Find the entrenched Article the amendment machinery can actually reach. Find the mechanism that stops working when the volunteers get bored in year forty. Find a sentence that makes VayuWeb sound more private, more available or more censorship-resistant than the design supports — that last one is a bug, and a serious one.

[Review the Constitution](https://github.com/johalputt/VayuWeb/blob/main/constitution/CONSTITUTION.md) [Report a defect](https://github.com/johalputt/VayuWeb/issues/new/choose) [Propose a change](https://github.com/johalputt/VayuWeb/blob/main/docs/spec/VWIP-0000.md)

---

## A constitution written before the code.

A naming system inherits whatever politics it was built with, and retrofitting governance onto shipped infrastructure has never once worked. So the rules come first, in public, where they can be attacked while changing them is still cheap.

The VayuWeb Constitution sets out an operative bill of rights, the registry and naming law, the amendment machinery and its entrenched limits, succession when the founder is gone, and the celebrated right to fork — the last check that survives when every other one has failed. Its text is dedicated to the public domain, deliberately: a licence on a founding charter is a leash on a fork.

[Read it](https://github.com/johalputt/VayuWeb/blob/main/constitution/CONSTITUTION.md) [Threat model](https://github.com/johalputt/VayuWeb/blob/main/docs/THREAT-MODEL.md)

---

## Phases, not dates.

Nothing here is promised by a calendar. Each phase has an acceptance test instead, so it is obvious from the outside whether it is actually done.

### Phase 0: Charter and specification (in progress)

The Constitution, the protocol specifications, the threat model and the VWIP process — published and open to attack.

### Phase 1: Registry core

Hypercore and Hyperbee, the record schema, sign and verify, proof-of-work. Done when a command line can register and resolve a name against a local log.

### Phase 2: Peer replication

Hyperswarm discovery and the convergence rule. Done when independent peers agree on the same registry state under conformance tests.

### Phase 3: Resolution proxy

The local proxy, its cache and its error catalogue. Done when an unmodified browser renders a VayuWeb page end to end.

### Phase 4: Hosting

Helia integration, the publish flow and pin management. Done when a site can be published and fetched by a second machine that was never told where it came from.

### Phase 5: Desktop client

Tauri 2, keys in the operating system keychain, register and publish and browse. Done when someone who has never used a terminal completes the whole flow.

### Phase 6: Radicle and independent implementations

Development moves to its permanent home. Done when a second, independently written implementation interoperates — the only real proof a specification is finished.

### Phase 7: Browser extension and mobile companion

Convenience layers, built last on purpose, so that neither ever becomes a requirement.

---

## What VayuWeb does not promise.

A project that overclaims dies the first time reality tests it. These are stated here as plainly as they are stated in the specifications.

### It does not make you anonymous

It removes intermediaries from naming and hosting. It does not hide your traffic from your network provider. Use Tor for the part VayuWeb does not solve.

### It does not guarantee availability

Content lives while at least one peer pins it. If everybody stops pinning, it is gone. There is no uptime promise, because nobody is in a position to make one.

### It cannot forget

The registry is an append-only log. You can withdraw a pointer and unpin your copy; you cannot compel other peers to discard bytes they already hold.

### A lost key is a lost name

There is no support desk, because a support desk that can restore your name is a support desk that can take it.

> “VayuWeb is the peer-owned root of the free internet.”

The code is not written yet. Specification review and adversarial critique are worth more than pull requests at this stage — find the clause a bad actor could drive a truck through, and say so.

[Watch the repository](https://github.com/johalputt/VayuWeb) · [Read the specifications](https://github.com/johalputt/VayuWeb/tree/main/docs/spec)
