---
kicker: For studios & agencies
---
## Host every client on one box — and hand each owner their own key

One binary already runs a website, mail, analytics and the admin. It now runs them per domain: every registered domain gets its own website, its own certificate, its own branded mailboxes and its own visitor figures — and the person who owns that domain gets a login that reaches their site and nothing else. No second install, no per-client control panel, no new thing to operate.

one binary· one modest VPS· one SQLite file

studio.example your install · full VayuOS

client-one.com own website own certificate branded mail own visitors

client-two.co.uk own website own certificate branded mail own visitors

client-three.in own website own certificate branded mail own visitors

### Every domain, its own website

Uploaded bundles live per domain, and the site mode, template and content moved into each domain's own record. Before this, one uploaded website served every registered domain — a studio could host exactly one client. The primary domain keeps its historic path, so an install that already deployed a bundle serves the same site after upgrading.

### A login confined by refusal

A client is bound to exactly one domain and reaches only the pages explicitly declared for them; anything undeclared is refused. The old default was permissive, so a page added next year would have been reachable by every client with no diff that looked like a security change. A surface entry that declares no audience halts the process at start-up rather than quietly meaning something.

### “My site” — the page they own

Their address, whether the connection is secured, whether mail is set up, what the site serves, and the one thing they can change: name, tagline, description and accent colours. Deliberately not an editor — the save and delete primitives are install-wide with no per-record ownership check, so “edit your own pages” would be selling a control the code cannot honour.

### Their visitors, not everyone's

Thirty days of page views and their busiest pages, filtered by domain in SQL rather than fetched broadly and narrowed afterwards. Two clients who both published `/about` previously had their counts added together — not a missing feature but a wrong number, and another client's traffic shown as this one's.

### Mailboxes you meter

Grant each domain a number of branded mailboxes; creation refuses at the cap. An allowance of 0 means none granted, never unlimited, and a domain that cannot be resolved fails closed — a limit that depends on a lookup succeeding is not a limit. Creation stays operator-only: mailboxes are made on request, by you. The allowance, the client's login and the handover are all set from the domain's own page — the only thing that needs a shell is the emergency override, deliberately.

### Mail you can hand over

Once a mailbox is handed to its owner, six routes into it close and one remains — a command-line override that cannot run without writing a permanent record the client reads on their own page. The difference between an escape hatch and a back door is that both exist, and only one leaves a mark nobody can remove. You hand a mailbox over from its own card in the panel, by retyping the address: it is one-way, and nothing reverses it.

### Six doors close

Each one is refused in the store or the engine, not in a handler — there are two handlers and a CLI above most of them, and a refusal in one is a refusal somebody routes around without meaning to.

Reading it from the panel. Every mailbox read now carries a typed authority, and an unset one is refused rather than assumed.

Your own console password over IMAP, POP3 and submission.

Resetting its password from the admin side. The holder's own recovery paths are untouched.

Turning off its second factor — reset, clear, sign in was the whole bypass.

Minting an app password for it — the quiet one. A credential reads the entire mailbox over IMAP with no record at all.

Pointing its mail elsewhere. Forwarding copies every future message without ever opening the box, and leaves the archive looking untouched.

### One stays open, and it leaves a mark

A client locked out on a Friday still has to be helped. So the last door is not sealed — it is wired to an alarm that cannot be disconnected.

A command-line override resets the mailbox password from the host. It refuses to run if the record cannot be written first — a break-glass whose log is optional is an administrator reset with a louder name.

The entry is append-only and hash-chained in the database, the handover itself is one-way, and both are enforced by database triggers — a promise the party running the database can undo with one `UPDATE` is not a promise.

The client sees it on their own page, a notice goes to a recovery address outside this install, and a copy is filed into the mailbox itself. Every app password on the mailbox is revoked by the same reset.

That outside address is required before a mailbox can be handed over at all. Without one the notice could only be filed into the mailbox — where whoever used the override can delete it, which is the opposite of a record.

### And what it deliberately is not

A business model built on a capability nobody has read the edges of is a business model that meets those edges in front of a client. These are the edges, in the same words the design record uses.

#### Not encryption

The messages are readable files on a server you operate. Anyone with direct access to that machine, its database or a backup can still read them, and no ledger records that. Only encryption under a key the server does not hold would prevent it — that version is fully designed and deliberately unshipped, on business grounds rather than cryptographic ones: custody, key-loss rates over sixty clients, and no self-service export or import path yet.

#### One process, one box

Row scoping is not a sandbox. A remote-code-execution bug reaches every client at once, and a hardware failure takes them all down together. Off-box encrypted backups with rehearsed restores are mandatory here, not optional. The DKIM signing key is one key, shared — deliverability is per-domain and correct, the key is not.

#### Metadata and routing stay yours

Sender, recipient, subject, size and delivery logs remain visible regardless of handover. You own MX and address creation, so future mail can be redirected even where the existing archive cannot be opened. Handover is a bound on reading a mailbox, not on running the mail system.

#### Tamper-evident, not tamper-proof

You own the database. Chaining the access record makes an edit *show up*; it does not make one impossible, and the product says so rather than implying otherwise. A record that could be quietly rewritten would be worse than none, because it would be believed.

The full design record — thirty-eight findings from three adversarial lenses, and why cryptographic sealing lost on the business rather than the mathematics — is [ADR-0152](https://github.com/johalputt/VayuPress/blob/main/docs/adr/ADR-0152-agency-hosting.md).
