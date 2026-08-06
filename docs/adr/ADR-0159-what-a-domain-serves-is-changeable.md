# ADR-0159 — What a domain serves must be changeable after it exists

- **Status:** Accepted; shipped in v3.17.17
- **Date:** 2026-08-06
- **Extends** ADR-0132 (VayuDomains multi-domain) and ADR-0154 (one console per
  site).

## 0. The report

> If I want website, blog and mail on a domain there is no option for new
> domains. Make a good option so I can easily activate and deactivate mail
> service on any domain.

## 1. What was actually wrong

Nothing in the data model. A domain has carried `site_type` and `mail_enabled`
since Stage 1, and the combination the operator wanted — a website at `/`, the
blog at `/blog`, and branded mail — has always been representable as
`business_subpath` with `mail_enabled` on.

`domain.Registry.Update(ctx, id, siteType, mailEnabled)` has also existed the
whole time, complete with its own primary-domain refusal and cache
invalidation.

**No handler ever called it.** Both values were therefore set once, on the "Add
a domain" form, and frozen for the life of the domain. The configuration was
representable, reachable at creation, and unreachable ever after.

That is a distinct failure from a missing feature, and it is worth naming: a
write path with no caller looks finished from every angle except the one that
matters. It compiles, it has tests, it is exported, and it does nothing.

## 2. What was built

**A `serves` endpoint** — `POST /os/api/domains/{id}/serves`, taking the site
type and the mail switch together. Together rather than separately because they
are one decision on the page; two endpoints would let a client apply half of it
and leave the panel describing a state the registry never reached.

**A "What this domain serves" card**, first in Site administration, because what
a domain does is the question every other row on that page assumes an answer to.

**Outcome language, not enum names.** `business_subpath` tells an operator
nothing. "Business + /blog — website at the root, blog at /blog" tells them what
a visitor will see, which is the question they are actually answering. The "Add
a domain" form now reads the same way; the two surfaces had been written years
apart and described one choice in two vocabularies.

## 3. The claim that had to be right

`mail_enabled` is a **provisioning and presentation** flag:

- `setup-vayudomain.sh` adds `mail.<host>` to the domain's certificate when it
  is on, and only when that name resolves;
- the client's own site page shows or hides its mail card;
- the DNS page lists the domain's mail records;
- the mailbox allowance means something only while it is on.

It is **not read by the mail stack**. `internal/vayuos/mail` serves accounts by
address and never consults the domain registry, so switching mail off does not
stop delivery to a mailbox that already exists.

The card says so explicitly. An operator who believes "off" means "stopped" will
reach for this as a kill-switch and discover otherwise at the worst possible
moment, which makes the wording a correctness problem rather than a stylistic
one. It also says that turning mail **on** is not finished when the button goes
green — the certificate needs a provisioning run — and points at the control
that does it.

Two states that look alike and are not: mail off for this domain, and mail off
for the whole install. The card distinguishes them and links to the page that
fixes each, because sending an operator to the wrong one is worse than saying
nothing.

## 4. What the primary domain does instead

Its site type is the install's own `site.mode`, owned by Settings and mirrored
into the registry at boot. The card refuses to edit it and says where the
control lives. Two controls on one value can only disagree, and a panel that
disagrees with itself is how an operator stops trusting the numbers on it.

The registry refuses the primary row too. The duplicate is deliberate: the
handler's refusal can name the page that does own the value, and the registry's
cannot.

## 5. What the audit found

Attacked and found sound:

- **Authorisation.** The route inherits `domains:write` through the `/os/api/domains`
  prefix, and `keyMayCall` is fail-closed for anything unmapped. A key granted
  `posts:write` is refused; a `domains:write` key is not. Client logins are
  refused by role before any of that.
- **Cache coherence.** `Registry.Update` already ends with `defer r.invalidate()`,
  so a saved change is visible on the next resolve rather than up to the
  registry TTL later. This was the most likely place for the feature to look
  broken while being correct, and it was already handled.
- **Escaping**, against a hostile hostname, and the primary-domain guard.

One improvement made during the pass rather than after it: the domain id travels
to the browser in a `data-id` attribute and is read from the DOM, instead of
being interpolated into JavaScript source. This codebase has already shipped a
site console where every control was inert because a script did not parse, and
a value spliced into source is the standard way that happens.

The save script is its own IIFE for the same reason. A parse error binds
nothing, so a mistake in this block cannot take Provision now, Issue login and
Save allowance down with it.

## 6. How it was proven

Seven mutations, all killed — including one that drops the "does not stop
delivery" sentence, one that removes the pre-selection of the current site type
(a picker that silently resets what it is showing), and one that hands the
primary domain an editable control.

The validator reads the same `siteTypeOptions` catalogue the form renders, so a
type added there is accepted without a second list to keep in step. Two copies
of one truth is how the theme exporter leaked a credential (ADR-0155 §6), and
the habit is worth repeating rather than relearning.
