# ADR-0162 — Mail listing is a cache, not an index

- **Status:** Accepted
- **Date:** 2026-09-24
- **Settles** item 3.1 of `docs/UX-AUDIT-2026-08-VAYUMAIL-VAYUTALK.md`
  (a SQLite message-header index), deferred there pending this decision.

## 0. The question

Listing a mail folder reads every message's headers from the Maildir. The audit
proposed moving those headers into a SQLite index so a listing becomes one
query. Before building it, the listing was measured.

## 1. The measurements

`Maildir.ListFolder` on one folder, Linux, files in the page cache (so the cold
column understates a real disk):

| Folder | Before: cold | Before: warm | After: cold | After: warm |
|---|---|---|---|---|
| 5,000 messages × 64 KB | 271 ms | 19 ms | 115 ms | 20 ms |
| 20,000 × 8 KB | 451 ms | 122 ms | 341 ms | 90 ms |
| 60,000 × 1 KB | 1.23 s | **1.11 s** | 1.18 s | **326 ms** |

Two faults, not a missing index, made the listing slow:

1. **A cold listing read every message whole** to parse its headers. A message
   carrying a 20 MB attachment cost 20 MB of reads to show one row.
2. **One cache bound for the whole install, reset whole when full.** At 50,000
   entries it was cleared entirely, so on any install holding more mail than
   that, a warm listing of a big folder was as slow as a cold one (the 1.11 s
   above). The larger the server, the more often this happened.

## 2. Decision

No index. The cache is fixed instead (`internal/vayuos/mail/maildir.go`):

- **Headers only.** `readHeaders` parses from a buffered reader and stops at the
  blank line; the body is never read to list a message.
- **Grouped by folder, evicted least-recently-listed.** The bound
  (`maxHeaderEntries`, 250,000 summaries — tens of megabytes) drops whole folders
  that nobody is looking at, never the one being listed, and a folder read
  entirely from the cache still counts as used.
- **Pruned on every listing.** Entries for files that were renamed (a flag
  change) or removed are forgotten, so a long-lived folder's cache holds exactly
  its current files.

Each rule has its own test in `header_cache_test.go`, and each test was seen to
fail when its rule was broken.

## 3. Why not the index

An index is a second copy of what the Maildir already says, and it is only
right if every writer updates it: SMTP delivery, the web console's move, flag,
pin and delete, IMAP and POP3 sessions, the retention sweep, mailbox retirement,
handover and restore from backup. A missed update does not make the list slow;
it makes it **wrong** — a deleted message still listed, a new one missing. The
cache cannot be wrong that way: every entry is checked against the file's size
and modification time on every listing, and a file that is not on disk is not
listed.

## 4. What would reopen this

A warm listing that stays over a second on real hardware, for a folder people
actually keep. The remaining cost is `readdir`, one `stat` per file and the
sort, which an index would remove. It would then need its own ADR, with the
writer list above as its test plan.
