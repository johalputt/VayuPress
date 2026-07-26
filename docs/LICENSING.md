# Licensing

VayuPress is licensed under the **Apache License, Version 2.0**. The full text is in
[`LICENSE`](../LICENSE); third-party components and their own terms are listed in
[`NOTICE`](../NOTICE).

Every Go source file carries an SPDX identifier:

```go
// SPDX-License-Identifier: Apache-2.0
```

That line is not decoration. Software-composition scanners and SBOM generators read per-file
identifiers, and a file without one is reported as *unknown licence* — which, to anyone deciding
whether they may depend on VayuPress, is indistinguishable from *proprietary*. The headers are
added and enforced by [`scripts/spdx-headers.py`](../scripts/spdx-headers.py), which runs in CI.

---

## Why Apache-2.0 and not MIT

MIT and Apache-2.0 are usually treated as the same choice in different lengths. They are not.
Apache-2.0 does four things MIT does not, and every one of them matters more to VayuPress than
the extra page of text costs.

**An express patent grant, and a patent-retaliation clause (§3).** Each contributor grants a
patent licence covering their contribution, and that grant terminates for anyone who initiates
patent litigation over the software. MIT grants copyright permissions and is silent on patents:
under MIT a contributor may hand over code today and assert a patent against its users later.
VayuPress implements DKIM, OpenPGP, MCP and OAuth 2.1, analytics and bot detection — dense
patent territory — so this clause carries real weight, and it carries more of it each year.

**An express trademark reservation (§6).** The licence grants copyright and patent rights and
explicitly withholds trademark rights. "VayuPress" stays protected while the code stays free.
MIT says nothing, leaving the same outcome to be argued from general trademark law rather than
from a clause the other party already accepted.

**Warranty and liability terms drafted for more than one jurisdiction (§§7–8).** MIT's
disclaimer is a single sentence written against US law decades ago. Apache-2.0's is structured
to survive review in the EU and India, which is where this project and its author actually are.

**An explicit inbound-equals-outbound rule (§5).** Contributions are licensed under the same
terms unless stated otherwise. That pairs exactly with the DCO this project already requires and
removes any ambiguity about what a merged pull request granted.

Apache-2.0 is OSI-approved, FSF-approved, GPLv3- and AGPLv3-compatible, and the default across
the Go and CNCF ecosystems. There is no successor version in preparation. It reads the same in
2126 as it does today.

---

## Copyright, the DCO, and the absence of a CLA

Copyright is held by **each contributor over their own work**. It is not assigned to any person
or company. Contributions are accepted under the
[Developer Certificate of Origin](https://developercertificate.org/), signed off per commit with
`git commit -s`.

**There is no Contributor Licence Agreement, and there will not be one.**

It is worth being precise about what that does and does not protect, because the usual summary
is wrong in both directions.

### What no-CLA genuinely forecloses

**Selling proprietary licences to this codebase.** A commercial dual-licence business — "the
free version is copyleft, pay us and you get proprietary terms with a warranty and an indemnity"
— requires the seller to hold or control the copyright in everything being sold. You cannot
warrant title over code you do not own. That is the machine MongoDB, Elastic and HashiCorp built
their monetisation on, and every one of them collected copyright through a CLA or an assignment
first.

Because VayuPress never took one, that path is closed. Permanently, and to its author as much as
to anyone else. Given that this project is funded by donations rather than licence sales, closing
it costs nothing and removes the incentive that has bent so many similar projects.

### What no-CLA does *not* prevent

**It does not prevent the outbound licence from changing on future versions.** Apache-2.0 §4
says so in terms:

> You may add Your own copyright statement to Your modifications and may provide additional or
> different license terms and conditions for use, reproduction, or distribution of Your
> modifications, or for any such Derivative Works as a whole, provided Your use, reproduction,
> and distribution of the Work otherwise complies with the conditions stated in this License.

"Derivative Works as a whole" is the operative phrase. Permissive licensing exists precisely to
allow this. A CLA is not what makes a forward relicensing possible under a permissive licence;
it is what makes *proprietary dual-licensing* possible. Those are different powers and they get
conflated constantly.

### What nobody can ever do

**Retract the grant on a version already published.** Every release that has shipped under
Apache-2.0 stays under Apache-2.0 for good. Anyone may fork from any published tag and continue
under those terms regardless of what this repository does afterwards. That is the guarantee that
actually protects a user who has deployed VayuPress, and it does not depend on anyone's goodwill.

---

## How the licence could change in future

There are exactly four routes. Three are open; the fourth is closed on purpose.

### Route A — forward-only move to a reciprocal licence (AGPL-3.0 or similar)

**Available now, requires nobody's consent, and is the only route worth seriously considering.**

Apache-2.0 is one-way compatible with GPLv3 and AGPLv3: Apache-2.0 code may be incorporated into
an AGPL-licensed work, and the combined work distributed under AGPL. (It is *not* compatible with
GPLv2 — the patent-termination and indemnification clauses conflict — so GPLv2 is not on the
table and never will be.)

**Why anyone would.** Apache-2.0 permits a hosting provider to take VayuPress, improve it
privately, run "Managed VayuPress" as a paid service, and publish none of the improvements. AGPL
§13 is the clause written for exactly that: an operator serving modified code over a network owes
its source to the users of that service. Grafana did this in 2021 and Matrix's Synapse in 2023,
both moving from Apache-2.0, and both were self-hosted server products of roughly this shape.

**What it would cost.** Corporate legal departments veto AGPL by policy, frequently without
reading it — including for an internal blog. VayuPress is aimed at individuals and small
organisations who want to own their infrastructure, and a licence that gets a project struck off
a procurement list before anyone evaluates it works against that. There is also a real question
for the `/mcp` endpoint and the public API: an AGPL obligation attaching to a self-hoster running
those is a compliance burden landing on precisely the people this project exists to serve.

**Conditions if it is ever done.** Apache-2.0 §4 must still be satisfied for the existing code:
ship the Apache-2.0 licence text, keep `NOTICE`, retain existing notices, and mark modified files
as changed. Beyond the legal minimum, the honest way to do it is:

1. **Forward only.** Never a retroactive claim over shipped releases.
2. **At a major version**, so the boundary is unambiguous.
3. **Announced before it happens**, not discovered in a diff.
4. **With contributors asked to agree** even though their agreement is not legally required.
   Nicolò Santilio's Caddy provisioning and CSRF fix were contributed to an Apache-2.0 project;
   the fact that the licence permits changing the terms around them is not a reason to do it
   without asking.

**Current assessment: do not.** The reasoning is in the next section.

### Route B — a licence *exception* rather than a licence change

If a reciprocal licence were ever adopted, specific carve-outs (a linking exception, or an
additional permission under GPLv3 §7) could relieve the parts where copyleft creates friction
without serving the purpose — plugin interfaces and the API surface being the obvious candidates.
This is a refinement of Route A, not an alternative to it.

### Route C — moving to a more permissive licence (Apache-2.0 → MIT)

Permitted by §4, and a bad idea. It would discard the patent grant, the trademark reservation and
the multi-jurisdiction disclaimers — everything listed above — in exchange for a shorter file.
Recorded here only so the option is visibly considered and visibly rejected.

### Route D — source-available (SSPL, BUSL, Elastic License)

**Closed.** Not by law — Apache-2.0 §4 would permit it — but by decision, recorded here so that
the decision has a date and a place. These licences are not open source under the OSI definition,
and adopting one would break the guarantee that VayuPress exists to offer: that the person
running it owns what they are running. Every project that has taken this route did so to protect
revenue from a hosted service. VayuPress has no such revenue to protect, and the absence of a CLA
means there is no dual-licence business waiting on the other side of it.

If a future maintainer wants this, the honest thing is to fork under a new name rather than
convert a project that people adopted on a different promise.

---

## The current recommendation

**Stay on Apache-2.0.**

The reasoning is specific to what VayuPress is, and it differs from the answer for a protocol.

A hosting provider running a closed, improved VayuPress does not threaten VayuPress. Every
self-hosted install keeps working; there is no network effect to capture and no ecosystem
position to lose. The harm is that improvements do not come back — real, but not existential, and
not something this project is structured to be damaged by, because it is not selling hosting and
so is not losing revenue to a competitor.

Weighed against that: AGPL would cost adoption among exactly the users this project is for, and
would put a compliance obligation on self-hosters running the MCP and API surfaces. The
[Governance Constitution](../GOVERNANCE-CONSTITUTION.md) also commits to a GPL/AGPL-free
dependency chain, and while an AGPL project may consume Apache-2.0 dependencies without
difficulty, flipping the project's own licence while keeping that clause would need the clause
revisited rather than quietly left in place.

The protections that matter here are already in force and are not licence choices at all: the
patent grant, the absence of a CLA, per-file SPDX identifiers, a published CVD process, a
CycloneDX SBOM, signed releases, and the irrevocability of every grant already made.

**Reassess if** VayuPress ever offers paid hosting itself (the calculus changes the moment a
competitor's closed fork takes revenue), if a hosted derivative appears at a scale that visibly
diverts contribution away from the project, or if the plugin ecosystem grows large enough that
keeping plugins free becomes a goal in its own right — the argument that built WordPress's
sixty-thousand-plugin commons.

---

## See also

- [`LICENSE`](../LICENSE) — the Apache-2.0 text, in force
- [`NOTICE`](../NOTICE) — third-party components and their terms
- [`CONTRIBUTING.md`](../CONTRIBUTING.md) — the DCO and how to sign off
- [`SECURITY.md`](../SECURITY.md) — coordinated vulnerability disclosure
- [`docs/CRA-READINESS.md`](CRA-READINESS.md) — EU Cyber Resilience Act assessment
- [`GOVERNANCE-CONSTITUTION.md`](../GOVERNANCE-CONSTITUTION.md) — the binding project rules
