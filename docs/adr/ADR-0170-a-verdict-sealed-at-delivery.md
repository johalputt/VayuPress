# ADR-0170 — The sender's check is sealed at delivery, and the reader says what it means

- **Status:** Accepted
- **Date:** 2026-10-31
- **Code:** `internal/vayuos/mail/verdict.go`, `cmd/vayupress/vayuos_mail_trust.go`, `cmd/vayupress/vayuos_mail_pictures.go`.

## 0. The question

Whether a message really came from the domain in its From line is decided
once, by the inbound server, when the message arrives (SPF, DKIM and DMARC
results). The reader runs much later, on a stored file. A header in the file
saying "checked" proves nothing: anyone can write that header into a
message, or copy a genuine one onto another.

## 1. Decision

**The verdict is sealed onto the message at delivery, with a key only the
install holds.** When the From domain authenticated the message, delivery
puts one header first, `X-VayuMail-Checked: <domain>; mac=<HMAC>`, an
HMAC-SHA256 over the domain and the message as delivered, keyed by a key
derived for this purpose alone (`mail-verdict-v1`). The inbound server
removes any sender-written `X-VayuMail-*` header before that. The reader
believes the header only when its MAC is right for that domain and those
bytes (`CheckedDomain`), so a header written by anyone else, or copied from
a genuine message onto another, does not read as one. Without a key nothing
is sealed and nothing reads as checked.

**The reader says it in one quiet line** under the sender:

- "shop.example confirmed it sent this", from the sealed verdict;
- "2 trackers blocked", for pictures that exist only to report the opening:
  a remote picture drawn two pixels wide or high or less
  (`isTrackerImg`). Such a picture is never fetched, even when pictures are
  loaded;
- an amber line when a link's words name one site and it goes to another
  ("A link says paypal.com but goes to paypa1.com"), or when the sender's
  name is another site's address, saying first when the sender is not in
  the holder's contacts.

**Links lose their tracking parameters**: the `utm_` family and the click
identifiers advertisers add are removed from every link a message shows,
with pictures on or off; every other parameter stays as written.

## 2. Consequences

- A message delivered before the seal existed shows no confirmation. It is
  not shown as unconfirmed either: the line says only what is known.
- The key is the install's; a message moved to another install keeps its
  header but no longer reads as confirmed there.
