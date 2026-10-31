# ADR-0173 — Help from a model in Mail, with its cautions enforced on the server

- **Status:** Accepted
- **Date:** 2026-10-31
- **Code:** `internal/vayuos/mail/modelhelp.go`, `cmd/vayupress/vayuos_mail_modelhelp.go`.

## 0. The question

Summing up a long conversation and drafting an answer are the most asked-for
help in mail, and the install may have a model. But mail is the most private
text an install holds: what leaves for a model, and to where, has to be
decided by the server and said to the holder, not left to the page.

## 1. Decision

**Off until the mailbox's holder turns it on.** The reader then offers to sum
up the conversation open and to draft an answer from a line, each button
naming where the text goes ("on this server", or the provider).

**A model counts as on this server only at a loopback address.** One
anywhere else, the install's own included, is used only for a mailbox an
administrator allowed (`model_outside`).

**To a model outside**, addresses, phone numbers and card-like numbers are
replaced before sending and put back in its answer, and an encrypted or
confidential message is never sent, the earlier messages of the conversation
included.

**Bounded.** Only the conversation open is sent, at most five of its
messages; only the holder's own mailbox is ever helped, never one an
administrator is reading. Every answer is a suggestion, and Reply with this
opens a reply holding it. Each request is counted against the mailbox with
where it went (`model_sent`, `model_sent_to`), which its administrator sees
on its Settings, never the text.

## 2. Consequences

- A page that asked for more, or for another mailbox, gets nothing: each rule
  is checked where the text is read, not where the button is drawn.
- Masking is a reduction, not a guarantee: names and free text still leave
  for a model outside, which is why an administrator must allow one.
