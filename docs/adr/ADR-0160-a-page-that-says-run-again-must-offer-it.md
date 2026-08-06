# ADR-0160 — A page that says "run again" must offer a way to run again

- **Status:** Accepted; shipped in v3.17.18
- **Date:** 2026-08-06
- **Extends** ADR-0154 (one console per site).

## 0. The report

> I have pointed the domain to the server but on the server it is not showing.
> There is no option for refresh, so I deleted it from VayuPress and re-added
> it — that works perfectly.

Deleting a domain to refresh a status line is the panel admitting it has no
refresh.

## 1. What was there

The certificate diagnostic ran its checks on page render and printed, among
them:

> **DNS answers for `<host>`** — the name does not resolve — point it at this
> server on Domains & DNS, **then run again**

There was no control anywhere on that page that ran anything. A grep across the
whole diagnostic for a re-check or refresh action returned one unrelated
sentence. The available actions were:

- **reload the browser**, which does re-run the checks but is not offered,
  described, or obviously the answer; and
- **Provision now**, which requests an entire root-side provisioning run to
  answer a question a DNS lookup answers in milliseconds.

So the copy instructed an operator to do something the page did not let them do,
and the workaround they found — delete the domain, add it again — worked, which
is the worst possible outcome: a destructive step that succeeds teaches itself.

## 2. Why the answer can persist after the fix

`boundedLookup` caches nothing. It is a fresh `net.Resolver.LookupHost` on every
render, so the application never holds a stale answer.

The staleness belongs to the **machine**. A negative DNS reply is cached by the
system resolver for the zone's SOA minimum — commonly several minutes — and
nothing in this process can invalidate another process's cache.

That matters for the design. A re-check button that silently returns the same
answer, with no explanation, teaches an operator that the button does not work
and sends them back to delete-and-re-add. Two honest sentences prevent that,
where a cleverer implementation could not: this product cannot clear the
resolver's cache and should not imply otherwise.

## 3. What was built

**Re-check now**, on the diagnostic itself. An HTMX `GET` that re-runs every
check and swaps the panel in place.

It is a `GET` and carries no CSRF token, because it changes nothing: it resolves
a name, reads the provisioning log and probes this server's own challenge path.
Putting a read behind a token makes it fail for a reason an operator cannot act
on.

**A timestamp** — "Checked at 09:41:07 UTC" — so "is this stale?" is answerable
rather than guessed.

**The resolver-cache caveat, shown only when DNS is the blocker.** Advice for a
problem an operator does not have is the noise that makes the rest go unread.

## 4. What the mutation pass changed

Six mutations. Two findings worth recording, because one of them was a defect in
the test rather than the code.

**A weak assertion survived.** `isDNSCheck` originally matched the label prefix
`"DNS "`, and the mutation that replaced it with `strings.Contains(label, "DNS")`
passed every case in the first version of the test — none of the fixtures
contained "DNS" anywhere except the real row. The looser predicate would show
the resolver-cache caveat on any page whose rows happen to mention DNS,
including pages where DNS resolves perfectly.

Both were tightened: the check now matches the row's actual opening (`"DNS
answers"`), and the test asserts against labels that a substring match would
wrongly accept — "Your DNS provider is not supported", "DNSSEC is enabled for
this zone", "Check the DNS records on Domains & DNS".

**Three mutations did not compile** — removing a branch left its flag unused —
and were re-run as variants that force the flag instead. A build failure is not
a killed mutation, and scoring one as a kill is how a gap gets recorded as
covered.

## 5. The rule

**Copy that tells an operator to do something must be accompanied by the control
that does it.** "Then run again", "re-run the installer", "try once more" are
instructions; without an affordance beside them they are the product describing
work it is declining to offer.

And the corollary this incident supplies: **when the operator's workaround is
destructive and it works, the missing affordance has already cost something.**
The delete-and-re-add cycle succeeded every time, which is precisely why it
would have kept being used.
