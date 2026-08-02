# Continuity — what happens to a client's site and mail if the studio stops

This document exists because ADR-0152 said it was worth more to a client than
the privacy feature, and it was written after that feature rather than before
it. That ordering was wrong, and stating so here is cheaper than pretending the
sequence was deliberate.

It answers one question, asked by someone non-technical who has handed their
business website and their business email to a small studio:

> If you disappear, what happens to my mail?

"Disappear" covers the boring cases as well as the grim one: a two-week
hospital stay, a lost laptop, a studio that stops trading, a falling-out. The
plan does not distinguish between them, because the client's exposure is the
same in all of them and a plan that only works for the polite cases is not a
plan.

Two audiences. Sections 1–4 are written for the **studio** and are operational.
Section 5 is written to be **handed to a client** as-is.

---

## 1. What the client actually owns today

Being precise here matters more than being reassuring, because everything below
depends on it.

| Thing | Who holds it | What happens if the studio vanishes |
|---|---|---|
| The domain name | **Whoever the registrar says** | If registered in the studio's name, the client may lose the address their mail depends on |
| DNS and MX records | The studio, in practice | Mail stops arriving when they lapse or point nowhere |
| Website content | On the studio's server | Recoverable from a backup, or re-buildable |
| Mail already received | On the studio's server, as maildir files | Recoverable **only** from a backup, or by the client pulling it over IMAP first |
| Mailbox passwords | The client, if handed over | Unaffected — this is what handover is for |
| The server itself | The studio's hosting account | Stops at the next unpaid invoice |

The two rows that end a business are the first and the fourth. A client who
loses the domain loses the address; a client who loses the archive loses years
of business records. Everything else is inconvenience.

## 2. The one thing to do before anything else

**Register the domain in the client's own name, with the client's own
registrar account, and give them the login.** Bill them for the renewal or
absorb it — that is a commercial choice — but do not hold the registration.

This single step converts every scenario below from "the client is stuck" to
"the client is inconvenienced", because it is the only asset that cannot be
reconstructed. Content can be rebuilt. Mail can be restored from a backup or
pulled over IMAP. A domain held by a company that no longer answers email is
recoverable only through the registrar's dispute process, if at all.

If a client's domain is currently in the studio's account, move it. That is
the highest-value hour of work in this document.

## 3. What already works, without writing any new code

### 3.1 The client can pull their own mail down, today

VayuMail speaks **standard IMAP** (implicit TLS on 993). Any mail client —
Thunderbird, Apple Mail, Outlook — can download the entire archive to the
client's own machine.

**It is an app password, not the mailbox password.** Device approval is on by
default (ADR-0129), which retires the raw mailbox password on the mail path, so
a client who types their normal password into Thunderbird gets a refusal and
reasonably concludes the mail server is broken. The holder mints an app
password themselves from the Connect tab in webmail.

That the *holder* can mint it is what makes this survive handover.
`canManageAppPassword` answers `isOwner` **before** it reaches the severance,
so the operator loses the ability to mint a credential for a handed-over
mailbox while the holder keeps it. A mail-only session can reach
`/os/vayumail`, so nothing about being a confined mailbox login blocks the
Connect tab either. Verified in both places rather than assumed — had either
gone the other way, this whole section would be advice that fails on the one
day it is needed.

This is the client's self-rescue path and it should be set up on **day one**,
not on the day it is needed. A local copy in a mail client is a backup the
studio cannot lose, cannot be locked out of, and does not have to be asked for.

### 3.2 Backups exist and are verified, not merely enabled

VayuKeep replicates the whole data directory — database, settings, media and
**maildirs** — to a target the operator owns, and every generation is
encrypted; the engine refuses to start without a passphrase rather than
silently writing something readable.

More importantly it runs a **restore drill**: it restores the newest generation
into a temporary directory, opens the database inside it, asks SQLite whether
the pages are intact, and throws it away. The distinction that matters is
between "replication is enabled", which is a configuration value worth nothing,
and "a restore worked at 04:12 this morning", which is a measurement. The panel
reports the second and is able to say no.

Turn it on. Check the drill result, not the toggle.

### 3.3 Content is exportable to a file

```
vayupress migrate export --file client-site.vaybundle
vayupress migrate import --file client-site.vaybundle --mode=merge --dry-run
```

The bundle is checksummed and offline-movable, and the import supports a dry
run. It carries **content only** — articles and pages. Accounts, mailboxes, PGP
keys and chat identities deliberately never cross.

### 3.4 There is no lock-in to unlock

The whole product is one Apache-2.0 binary and a SQLite database, published on
GitHub. A successor — the client's nephew, another agency, a hosting company —
can run the same binary against a restored data directory. There is no
proprietary format to reverse-engineer and no licence to transfer.

## 4. The runbook

### 4.1 Studio unavailable, temporarily (illness, travel, no laptop)

Nothing breaks by itself. The install keeps serving and delivering mail
unattended; certificates renew automatically. The exposures are a hosting
invoice that fails to be paid, and nobody watching for a full disk.

**Prepare:** put the hosting account on a card that will not expire, and give
one trusted person the ability to pay it.

### 4.2 Studio unavailable, permanently

The client needs three things, in this order:

1. **The domain.** Covered by §2 if it was done. If not, this is the blocker
   and everything else waits on it.
2. **Their mail.** Immediately available over IMAP if §3.1 was set up. If not,
   it must come from a backup, which requires the backup location and its
   passphrase.
3. **The site.** Rebuildable from a backup or a content bundle; worst case,
   re-built from scratch. Painful, not fatal.

**Prepare:** the sealed envelope in §4.4.

### 4.3 Client leaves for another provider

An ordinary, planned migration. Export content with §3.3, let them pull mail
over IMAP with §3.1, and point DNS at the new host. Hand over rather than make
them extract — a studio that makes leaving difficult earns exactly one bad
referral for every client who tries.

Do **not** delete the mailbox until the client confirms their local copy is
complete. There is no undo.

### 4.4 The envelope

One document, held by the client or their solicitor, reviewed annually:

- The domain registrar, and the account it is registered under.
- Where backups are written, and how to reach that location.
- The backup passphrase. **Not in the same envelope as the location**, and
  never in email.
- The mailbox addresses, and their recovery addresses.
- The hosting provider and account.
- A one-line statement of what the software is, with the GitHub URL, so a
  successor knows what they are looking at.

That envelope, plus a domain in the client's own name, plus a mail client
holding a local copy, is a continuity plan. Everything above it is detail.

## 5. What to tell a client, in their words

> Your website and your email run on a server we operate for you. Here is what
> that means if something happens to us.
>
> **Your domain name is yours.** It is registered in your name, with your own
> account at the registrar, and you have the login. Nobody can take it, and it
> is the one thing that could not be replaced.
>
> **Your email is already on your own computer.** We set up your mail
> programme to download a copy of everything, using a device password created
> for that programme — not the password you type to read your mail on the web,
> which deliberately will not work there. If our server went away this
> afternoon, the mail you have received is still on your machine. Please do not
> switch that off.
>
> **We keep encrypted backups**, and we test them by actually restoring them
> rather than assuming they work.
>
> **You have an envelope** listing your domain, where the backups are, and who
> to hand it to. If we are not here, someone technical can take that and pick
> up where we left off. The software is public and free, so they are not locked
> out of anything.
>
> **What we cannot promise.** We are a small business, not a data centre. If
> the server fails you may be offline for a day while we restore it. Your mail
> is stored as ordinary files on a machine we run — it is not encrypted in a way
> that would stop us reading it, and we have told you that plainly elsewhere. If
> you need guarantees beyond this, you need a larger provider, and we will say
> so rather than pretend otherwise.

## 6. What this plan does not cover

Stated because a plan that only lists its strengths is one a client is right to
discount.

- **It is procedural, not enforced.** Nothing in the software checks that the
  domain is in the client's name, that the envelope exists, or that IMAP was
  ever set up. Every step in §2 and §4.4 is a habit, and habits lapse. The
  first three could plausibly become panel checks; today they are not.
- **There is no self-service mail export.** A client cannot press a button and
  receive their archive. IMAP is the substitute and it is a good one, but it
  requires a mail client configured in advance — and a client who never did
  that, whose studio has vanished, depends entirely on a backup someone else
  holds. ADR-0152 §D4 names this same gap as one of the reasons cryptographic
  sealing was not built; it has not closed since.
- **It assumes the backups are real.** Everything in §4.2 rests on VayuKeep
  being enabled, its target reachable, and its passphrase known. An operator who
  turned it on and never looked at a drill result has a plan resting on an
  assumption.
- **One box still fails as one box.** Row scoping is not a sandbox and a
  hardware failure takes every client down together, as ADR-0152 §D5 records.
  Continuity here means "recoverable", never "uninterrupted".
- **It has not been rehearsed.** No client migration has been performed against
  this document. A runbook nobody has walked through is a draft, and the first
  real use will find something wrong with it.

## References

- **ADR-0152** — agency hosting on one install; §D4 mail privacy, §D5 what is
  deliberately not isolated, open decision 7 (this document).
- `docs/OPERATIONS.md` — backup and restore operations.
- `docs/INSTALLATION.md` — what a successor needs to stand the binary up.
