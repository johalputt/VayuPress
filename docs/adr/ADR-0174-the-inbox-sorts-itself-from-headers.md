# ADR-0174 — The Inbox sorts itself from headers, on the server

- **Status:** Accepted
- **Date:** 2026-10-31
- **Code:** `internal/vayuos/mail/category.go`, `cmd/vayupress/vayuos_mail_list.go`. Builds on ADR-0162 (the listing is a cache).

## 0. The question

A busy Inbox mixes people with receipts, newsletters and notifications.
Sorting it is what makes it usable, and the usual way sends message text to
a classifier somewhere else.

## 1. Decision

**Each message is placed in Primary, Updates, Newsletters, Receipts or
Social from its headers and subject alone**, as the listing reads them
(`categorize`, run where the summary cache is filled, ADR-0162), first
match wins: a subject that reads as an order, a receipt or an invoice is a
Receipt; mail from a social network's own domain is Social; a message with
`List-Unsubscribe` or `List-Id`, or bulk or list precedence, is a Newsletter;
one from an automated sender (no-reply and its like) or marked
`Auto-Submitted` is an Update; everything else is Primary. Nothing leaves the
server and no body is read.

**Tabs only when they say something.** When the Inbox holds mail of two
kinds or more, the kinds are tabs over the list, each with its unread count;
All, the first, is the Inbox as before.

**Two views join the sidebar while they hold something**: Follow-ups, a
person's message read more than two days ago and not answered (counted from
the replies in Sent), and Unsubscribe, the newest message of each list
received, whose reader offers to leave it.

## 2. Consequences

- A rule that misfiles is fixed in one function and applies at the next
  listing, since nothing about the category is stored with the message.
- Categories are a view: a message is in the Inbox whatever its tab.
