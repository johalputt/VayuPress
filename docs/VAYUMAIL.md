# VayuMail

VayuMail is the install's own mail: mailboxes on your domains, served to
browsers in the console and to mail apps over IMAP, POP3 and SMTP, with
contacts and calendars synced to phones. It runs in the same process as the
site and needs no outside service.

This guide covers what a mailbox's holder sees and what an administrator
sets. Account recovery has its own guide, [MAIL-RECOVERY.md](MAIL-RECOVERY.md),
and so does taking mail elsewhere, [CONTINUITY.md](CONTINUITY.md). The
decisions behind the features below are ADR-0162 and ADR-0167 to ADR-0174.

## The app

With a mailbox open, the console's rail folds away and Mail is its own app:
**Mail** beside the site's mark goes to the Inbox, and the top bar's search
is Mail's (`/` reaches it), with where to look (this folder, all mail, from,
has attachments) under it while it has focus.

- **The list.** Hovering a message, or reaching it by keyboard, offers
  Archive, Delete, Mark read or unread and Snooze in place of its time. Pick
  several and the selection bar acts on them all, Label included.
- **The reader.** Its bar starts with Reply, Reply all and Forward; its tools
  say what they do in words and fold to icons as the reader narrows.
- **Compose** is the button at the head of the folders.

### The keys

| Key | In the list | In the reader |
| --- | --- | --- |
| `j` / `k` | Next / previous message | Older / newer message |
| `r`, `a`, `f` | | Reply, Reply all, Forward |
| `e` | Archive the picked | Archive |
| `#` | Delete the picked | Delete |
| `s` | Pin the picked | Pin or unpin |
| `b` | Snooze the picked | Snooze |
| `l` | Label the picked | Label |
| `Shift+U` | Mark the picked unread | Mark unread |
| `u` | End the selection | Back to the list |
| `g` then `i`, `s`, `d` or `a` | Inbox, Sent, Drafts or Archive | Inbox, Sent, Drafts or Archive |
| `w` | Full width, then the list again | Full width, then the list again |
| `Ctrl+Shift+M` (`⌘⇧M`) | Switch mailbox | Switch mailbox |
| `/` | Search | Search |
| `?` | All the keys | All the keys |

### On a phone

Mail is worked by the thumb: a message swiped right in the list is archived,
swiped left is snoozed until tomorrow, the row saying which as it follows the
finger; the list pulled down from its top refreshes; and Compose is a round
button above the tab bar. A read-only mailbox gets neither.

## The Inbox sorts itself

Each message is placed in **Primary, Updates, Newsletters, Receipts or
Social** from its headers and subject alone, on the server; no message text
leaves it (ADR-0174). When the Inbox holds mail of two kinds or more, the
kinds are tabs over the list, each with its unread count; **All**, the first,
is the Inbox as it always was.

Two views join the sidebar while they hold something:

- **Follow-ups**: a person's message you read more than two days ago and have
  not answered.
- **Unsubscribe**: the newest message of each list you receive, whose reader
  offers to leave it.

## What the reader tells you

One quiet line under the sender says what is known about where a message
came from (ADR-0170):

- **"shop.example confirmed it sent this"** when the sending domain
  authenticated the message on its way in. The confirmation is sealed onto the
  message at delivery with a key only this install holds, so one written by
  anyone else, or copied from a genuine message, does not read as one.
- **"2 trackers blocked"** when it carries pictures that exist only to report
  that it was opened. They are never fetched, even when pictures are loaded.
- An **amber line** when a link's words name one site and it goes to another
  ("A link says paypal.com but goes to paypa1.com"), or when the sender's name
  is another site's address, saying first when the sender is not in your
  contacts.

**Pictures** stay off until you ask; then they load through this server, so
the sender learns the message was opened but not your address or device.
**Links** lose their tracking parameters (the `utm_` family and advertisers'
click identifiers), whether pictures are on or off.

## Sending privately

- **To someone with a key**: mail is encrypted with OpenPGP when a key for the
  recipient is on file (Mail › PGP keys), and the compose bar says so.
- **To anyone: a sealed message** (Mail › Send a sealed message). It is not
  sent: it stays on this server, sealed with a passphrase you tell the
  recipient another way, and they are mailed a link. The passphrase opens it
  there for a day, a week or thirty days, and they can answer it on that page;
  the answer lands in your Inbox. Ten wrong passphrases close it for good, and
  you can close it at any time (ADR-0171).

## Throwaway addresses

Settings › Addresses makes an address for one website in a click
(`shop-x7k2qa@` your domain), shows which take mail and how much, turns one
off when the website passes it on, and removes it. An address turned off
takes nothing by any road: it is refused at the door, not filed under its own
name, and not caught by the domain's catch-all. A mailbox holds up to a
hundred (ADR-0172).

## Help from a model

When the install has a model and you turn it on (it is off until you do), the
reader offers to **sum up** the conversation open and to **draft an answer**
from a line, each button naming where the text goes. A model counts as on
this server only at a loopback address; one anywhere else is used only for a
mailbox an administrator allowed, and to it addresses, phone numbers and
card-like numbers are replaced, and encrypted or confidential messages are
never sent. Every answer is a suggestion: **Reply with this** opens a reply
holding it (ADR-0173).

## Settings

Mail's Settings is one page of sections, listed on the left under a field
that finds any setting by name: General, Addresses, Rules and replies,
Privacy and security, and for a mailbox's holder Signing in and Import and
export. The name on mail you send and the forwarding address save as they
change, with "Saved. Undo" for ten seconds.

## Signing in, and keeping it yours

Settings › Signing in opens with a **checklist and its score**: two-factor, a
passkey, a way back in, and no app password left unused, each with where to
put it right (ADR-0169).

- **A sign-in name.** Your address is public, so it is what guessers try.
  With a sign-in name set, the address no longer signs in with your password
  at any door; the name does. App passwords keep working with the address, so
  mail apps already set up are not broken.
- **Passkeys** sign in with your device's own lock, and keep working while
  sign-ins to your address are paused. With two passkeys you can turn your
  password off altogether (ADR-0168).
- **App passwords** for mail apps, and whether mail apps take app passwords
  only.
- **Where you are signed in**: every console session in your name, with its
  browser, system and time, a Sign out on each, and Sign out everywhere else.
- **Sign-in attempts on this mailbox**: when, from where (the address with its
  last part hidden), through which door and what happened, with a warning
  when the password was right and the code was not, and "It was me" on each
  source. You can limit sign-in to a list of countries.

If someone gets your password right and your code wrong, you are told at
once, at your address and your recovery address.

## For administrators

Mail's administration is tabbed: **Overview**, **Accounts** (mailboxes,
aliases, recovery), **DNS** (what each mail domain still needs), **PGP keys**
and **Security**. Failed sign-ins across the install, and the blocks on their
sources, are in **Shield › Sign-in attempts** ([VAYUSHIELD.md](VAYUSHIELD.md)).
