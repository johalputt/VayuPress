# ADR-0103: Member Experience — Welcome Emails, Account Surfaces, Threaded Comments & Reply Notifications

**Status**: Accepted
**Date**: 2026-06-29
**Author**: @johalputt
**Owner**: Core
**Relates to**: [ADR-0102](ADR-0102-unified-identity-and-bootstrap-admin.md)

## Context

VayuPress had a working but thin reader-membership layer: a passwordless
magic-link sign-in, a floating VayuPortal widget, and a flat comment list. In
production several rough edges undermined it:

- **Sign-up felt identical to sign-in.** A brand-new member received the same
  bare "here is your sign-in link" email as a returning one, with no welcome and
  no sense of having joined something. Both transactional emails were plain,
  unstyled, single-paragraph bodies.
- **VayuMail sign-in hid the console.** When a staff member signed in through
  the portal with their VayuMail mailbox, the login response omitted the mailbox
  role, so the "Open VayuOS console" shortcut only appeared after a full page
  reload — making it look like mailbox users had no console access.
- **The nav never reflected being signed in.** The public nav always showed
  "Sign in / Sign up", even to an authenticated member, with no name or sign-out.
- **Commenters had no way to track their own activity.** Once a comment was
  posted there was no surface to see where you commented or whether it was live.
- **Comments were flat.** There was no way to reply to a specific comment, and
  no notification when someone responded — so conversations didn't continue.
- **No member-facing notification or newsletter controls.** The member-level
  newsletter opt-in was disconnected from the actual subscriber list that
  broadcasts use.

## Decision

Treat the reader as a first-class member with a small, owned experience layer.

### Transactional emails

Redesign the `magic_link` and `welcome` email templates as polished, emoji-rich
HTML cards with a clear call-to-action, and add a new `comment_reply` template.
A **new** member gets the welcome email **in addition to** their sign-in link;
returning members get only the sign-in link. All three remain operator-editable
in the Tier-4 email-template editor and keep their plain-text fallbacks.

### VayuMail console shortcut

Factor the portal member snapshot (`memberSnapshot`) into one helper used by both
`GET /api/v1/members/me` and the VayuMail login response, so the mailbox role and
console flag are returned at login time. The "Open VayuOS console / VayuMail"
button now appears immediately, with no reload.

### Signed-in nav + activity

When authenticated, the portal widget collapses the nav "Sign in / Sign up"
links into the member's name, which opens the account panel (with sign-out). A
new **Activity** tab lists the member's own comments with a moderation-status
badge, backed by `GET /api/v1/members/comments` reading the **read pool**.

### Threaded comments + reply notifications

Add a nullable `parent_id` to `comments` (migration `052`). A reply must point
at an existing comment on the same article; the public widget renders one level
of indented replies with per-comment reply forms. The comment section is
restyled to inherit the active theme's Pico custom properties (avatars, rounded
cards, indented threads) so it blends into any installed theme.

When a reply is **approved**, the author of the parent comment is emailed via the
new `comment_reply` template — but only if they are a member who has reply
notifications enabled (`reply_notify`, migration `053`, default on) and it is not
a self-reply.

### Member notification & newsletter controls

The account portal gains a **Notifications** section: a reply-notification toggle
and a newsletter toggle. Saving the newsletter toggle now also syncs the public
`newsletter_subscribers` list — subscribing the member **confirmed** (no double
opt-in, since an authenticated member's address is already verified) or
unsubscribing them — so the choice affects real broadcasts.

### IndexNow publish gate (carried in this release)

`pingIndexNow` now verifies an article is `published` (via the read pool) before
submitting it, and `handleOSPostStatus` pings on a publish transition, so a
draft or unpublish never reaches IndexNow.

## Consequences

- **Positive.** New members feel welcomed; mailbox users see their console
  immediately; signed-in readers see themselves in the nav and can follow their
  own activity; conversations continue through replies and reply emails; members
  control their own notifications and newsletter membership from one place.
- **Privacy preserved.** Reply emails are opt-out per member and never sent for
  self-replies; the activity feed and member-comment query run on the read pool
  per the scale rules; no new third-party calls are introduced.
- **CSP intact.** The redesigned comment and portal widgets build all DOM via
  `createElement`/`textContent`, served same-origin — no inline handlers, no CDNs.
- **Schema growth.** Two additive, defaulted columns (`comments.parent_id`,
  `members.reply_notify`); both safe on existing rows. Down migrations retain the
  columns (SQLite `DROP COLUMN` is version-dependent).

## Alternatives considered

- **Deeper comment nesting.** Rejected for now: one level of replies keeps the
  UI legible and the data model simple; deeper threads can come later if needed.
- **Double opt-in for member newsletter toggles.** Unnecessary — an
  authenticated member has already proven ownership of the address, so a confirmed
  subscribe is correct and friction-free.
