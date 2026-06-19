# medium2vayu

A CLI tool to import a Medium HTML export into a VayuPress SQLite database.

## Features

- Reads a Medium export ZIP file or directory of HTML files
- Extracts title from `<h1>` or `<title>`, publication date from `<time datetime="...">`, and tags from `<a rel="tag">` links
- Slug derived from Medium export filename (`YYYY-MM-DD_slug_hash.html` format)
- Content HTML preserved and passed through — sanitized by VayuPress on render
- Draft detection from `class="draft"` or filename prefix
- `import` and `list` subcommands
- Idempotent `INSERT OR IGNORE` writes — safe to re-run

## Installation

```bash
cd tools/medium2vayu
CGO_ENABLED=1 go build -o medium2vayu ./cmd/medium2vayu
```

## Usage

```bash
# List all posts in the export (no write)
./medium2vayu list --input medium-export.zip

# Import into VayuPress
./medium2vayu import \
  --input medium-export.zip \
  --vayu-db /var/lib/vayupress/vayupress.db

# Import from a directory of HTML files
./medium2vayu import --input ./medium-export-dir --vayu-db vayupress.db

# Dry-run (parse and print, no write)
./medium2vayu import --input medium-export.zip --dry-run

# Include drafts
./medium2vayu import --input medium-export.zip --skip-drafts=false
```

## Exporting from Medium

1. Go to **Settings → Account → Download your information**
2. Request a data export — Medium emails a ZIP file
3. The ZIP contains one HTML file per post, named `YYYY-MM-DD_post-title_abc123.html`

## Notes

- Article IDs are generated fresh (Medium IDs are not meaningful in VayuPress)
- Tags are slugified from their display names
- If no publication date is found in the HTML, `time.Now()` is used
