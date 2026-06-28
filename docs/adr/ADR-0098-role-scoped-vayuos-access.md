# ADR-0098: Role-Scoped VayuOS Access for VayuMail Accounts

**Status**: Accepted  
**Date**: 2026-06-28  
**Author**: @johalputt

## Context

VayuMail accounts carry one of five roles — administrator, editor, author,
reviewer, mailbox — but those roles only governed mail capabilities (send/delete/
manage). The accounts could not sign in through the website login button (only
CMS users could), and there was no role-scoped console: a logged-in non-admin
saw the entire sidebar, and access was gated only coarsely (an admin-or-not check
on a handful of POST endpoints). The operator's need is the opposite: a mailbox
account should be able to log in and see *only* its mailbox; an author should see
*only* authoring; everything else should be both hidden and unreachable.

A partial bridge already existed (`resolveMailMember` / `mailConsoleAccess` /
`mailOnlyPathAllowed`) that confined a *membership-portal* mail session to the
VayuMail surface, but it did not cover the `/os` login form, did not scope the
console roles (author/editor) at all, and the sidebar was static.

## Decision

Introduce a single, ranked **console access level** derived from the identity's
role, and use the *same* policy for both what the sidebar shows and what the
route guard allows — so "what a role can see" is exactly "what a role can reach".

### Access levels

```
accessMailOnly < accessAuthor < accessEditor < accessAdmin
```

- **mailbox, reviewer → mailOnly**: confined to the VayuMail surface (inbox,
  message view, profile, sign-out, the static assets those need). Any other `/os`
  path redirects to the inbox.
- **author → accessAuthor**: Dashboard, Posts, New Post, Media, VayuMail, Profile.
- **editor → accessEditor**: the above + Comments, Pages, SEO, Analytics, Theme,
  Messages.
- **administrator → accessAdmin**: the full console (Members, Newsletter,
  Monetization, System, Operations, Settings, Security, API Keys, Update…).

`osPathMinLevel(path)` maps each `/os` (and `/os/api/...`) path to the minimum
level required, with author-level content as the permissive default so a new
benign page is never accidentally locked out; only the editor- and admin-
sensitive areas are gated.

### Login + session

The `/os` login form now falls back to authenticating against the VayuMail
account store (email + password, active accounts only, plus the account's TOTP
when enabled) and issues a session whose id is prefixed `vmail:`. The session
middleware resolves that prefix to a synthesized, role-scoped identity; because
resolution re-checks the account is still active on every request, deleting or
deactivating an account immediately invalidates its web sessions. CMS-user
sessions are unchanged.

### Enforcement + UI, one source of truth

`requireSessionOrAPIKey` computes the level once and, for every request, either
confines a mail-only session (`mailOnlyPathAllowed`) or blocks a console session
below the path's required level (browser → redirect to its allowed home; API/XHR
→ 403). The sidebar (`osSidebarNav`) renders each item only when the current
level satisfies the same `osPathMinLevel`, and omits empty sections. Legacy
API-key callers remain admin-equivalent.

## Consequences

- The five VayuMail roles become real, end-to-end identities: they log in from
  the website and use precisely the slice of VayuOS their role allows; everything
  else is hidden *and* server-side-blocked (defense in depth, not just UI hiding).
- This also tightens existing non-admin CMS users (author/editor), who are now
  scoped the same way rather than seeing the full nav. Administrators and the
  API key are unaffected.
- The visibility and enforcement rules share one function (`osPathMinLevel`), so
  they cannot drift apart; adding a new area means classifying it once.
- Role semantics for reviewer/mailbox remain "VayuMail only" (unchanged from the
  prior bridge); reclassifying them later is a one-line change in
  `mailConsoleAccess`.
