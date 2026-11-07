---
kicker: Companion tools
---
## Bring your content with you. Leave anytime, too

### Move in

`vayupress migrate markdown --dir ./posts` imports a folder of Markdown, front matter and all, and importers in `tools/` read Ghost, WordPress, Substack, Medium, Hugo, Jekyll and Notion: titles, slugs, dates, tags, images and drafts kept. The [migration guide](https://github.com/johalputt/VayuPress/blob/main/docs/MIGRATION.md) has every source.

### Back up and restore

`vayupress backup` writes an encrypted, checksummed archive and `vayupress restore` checks it before it puts anything back. The [backup guide](https://github.com/johalputt/VayuPress/blob/main/docs/operations/backup-restore.md) covers schedules and rehearsing a restore.

### Leave with everything

`tools/vayu-export` writes the whole site as static HTML, every post its own page with a paginated index, for an archive, a CDN or no server at all.

### Check an extension first

`vayu-compat check --manifest plugin.json` validates a plugin or a theme with the same rules the host enforces: hooks, permissions, sandbox limits and the page's security policy.
