# ADR-0087: Subscription Engine v2 — Revenue Analytics, Lifecycle & Activity Log

**Status**: Accepted
**Date**: 2026-06-26
**Author**: @johalputt
**Supersedes**: none (extends ADR-0068 Members, the premium memberships in
migration 037, and ADR-0086-era Members console)

## Context

VayuPress shipped a capable membership system: priced tiers, per-member
subscriptions, a passwordless reader portal, a public pricing page, member
labels, and a signature-verified Stripe webhook for paid upgrades (no embedded
payment SDK). The operator-facing Members console, however, showed only a thin
slice of the business: total / free / paid counts, a single MRR figure, and a
30-day signup count.

That is materially less than what creators expect from a modern subscription
product. To run a paid publication an operator needs to see **retention and
unit economics** (churn, conversion, ARPU, lifetime value), **revenue
movement** over time, and **what actually happened** (an activity trail) — and
they need the lifecycle primitives those metrics describe: free trials, and a
cancellation that lets a paying reader keep access until the period they already
paid for ends.

## Decision

Introduce a **Subscription Engine v2** entirely within the existing,
constitution-clean architecture (single binary, SQLite, no embedded payment
SDK, strict CSP, no inline styles in the public surface).

1. **An activity log is the source of truth for movement.** A new
   `member_events` table records every lifecycle moment — signup, subscribe,
   trial start, upgrade/downgrade, renew, cancel, scheduled cancel, comp, and
   failed payment — each with the monthly value at the time. Recording is
   best-effort and never blocks the membership action that triggered it. The log
   powers both a site-wide recent-activity feed and a per-member timeline, and
   feeds the 30-day MRR-movement and churn analytics.

2. **Deeper analytics are derived, not stored.** `Stats` is extended with
   trialing count, ARPU, estimated LTV, free-to-paid conversion rate, 30-day
   churn rate, new/churned/net MRR movement, and new-paid/canceled counts —
   computed from cheap aggregate queries over members, subscriptions and events.
   MRR now **excludes** subscriptions still inside a free trial so it reflects
   truly recurring revenue, with trialing members reported separately. A
   `RevenueByTier` breakdown attributes MRR across plans.

3. **Lifecycle primitives.** Tiers gain an optional `trial_days`; starting a
   subscription with a trial grants full access but contributes 0 MRR until it
   converts. Subscriptions gain `cancel_at_period_end`, and a new
   `ScheduleCancellation` keeps access until the period ends — the default
   "cancel" behaviour — distinct from the existing immediate cancel.

4. **Optional Stripe price wiring stays decoupled.** Tiers can store monthly /
   yearly Stripe Price ids so a hosted Stripe Checkout can be linked without any
   embedded SDK. The webhook additionally reconciles
   `customer.subscription.updated` (→ schedule cancel), `customer.subscription.deleted`
   (→ cancel), and `invoice.payment_failed` (→ at-risk event), keyed by Stripe
   customer id.

5. **The console becomes a dashboard.** The Members page leads with eight
   headline metrics, a dependency-free inline-SVG growth sparkline, a
   revenue-by-tier panel, a recent-activity feed, one-click CSV export, an
   instant client-side member search, and a per-member cancel action — all
   server-rendered within the strict `script-src 'self' 'nonce-…'` CSP.

## Guards

- `internal/members/subscription_engine_test.go` — trials excluded from MRR,
  scheduled cancellation keeps access, derived metrics (ARPU/conversion/churn/
  MRR-movement), the activity log ordering and global feed, comp vs. subscribe
  classification, revenue-by-tier, tier trial/Stripe-price persistence, and CSV
  export shape.
- Existing `internal/members/premium_test.go` continues to pass unchanged,
  proving backward compatibility of tier CRUD, the subscription lifecycle, MRR,
  and the Stripe upgrade path.

## Consequences

- Migration `040-subscription-engine` is additive (new columns default safely,
  new table created if absent); existing data upgrades losslessly. SQLite
  `DROP COLUMN` is version-dependent, so the down migration drops only
  `member_events` and retains the added columns (harmless if rolled back) —
  consistent with migrations 037 and 039.
- The Members console now reflects the full economics of a paid publication, and
  the activity log gives operators an auditable history of membership changes.
- Payments remain decoupled: VayuPress still embeds no payment SDK and only
  reacts to a signed webhook.
