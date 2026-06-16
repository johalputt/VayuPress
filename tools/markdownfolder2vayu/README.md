# markdownfolder2vayu

A CLI tool that imports a folder of Markdown files into a [VayuPress](https://github.com/johalputt/vayupress) SQLite database.

## Features

- Parses YAML frontmatter (`title`, `slug`, `date`, `tags`, `draft`)
- Automatic slug derivation from filename when not in frontmatter
- Automatic title extraction from first H1 heading when not in frontmatter
- Converts Markdown to HTML using [goldmark](https://github.com/yuin/goldmark) with GFM extensions
- Resume support: `INSERT OR IGNORE` skips already-imported articles
- Checkpoint tracking: stores last processed file path
- Dry-run mode: preview what would be imported without writing
- Skip drafts: omit files with `draft: true`

## Installation

```sh
cd tools/markdownfolder2vayu
go build -o md2vayu ./cmd/md2vayu
```

## Usage

### Import Markdown files into VayuPress

```sh
# Import all .md files from a folder
./md2vayu --dir ./posts --vayu-db /path/to/vayupress.db

# Preview without writing
./md2vayu --dir ./posts --vayu-db /path/to/vayupress.db --dry-run

# Include draft posts
./md2vayu --dir ./posts --vayu-db /path/to/vayupress.db --skip-drafts=false

# Non-recursive (top-level only)
./md2vayu --dir ./posts --vayu-db /path/to/vayupress.db --recursive=false
```

### List Markdown files (no DB write)

```sh
./md2vayu list --dir ./posts
```

## Frontmatter format

```yaml
---
title: My Post Title
slug: my-post-title
date: 2024-01-15
tags:
  - go
  - markdown
draft: false
---

Post body here...
```

All frontmatter fields are optional. Fallbacks:
- `slug` → derived from filename (lowercase, hyphens)
- `title` → first H1 heading in body
- `date` → file modification time

> **Note:** Only YAML frontmatter (between `---` delimiters) is supported. TOML frontmatter is not supported.

## Progress output

```
Scanning /path/to/posts...
Found 42 Markdown files.
[  1/42] my-first-post.md → "My First Post" (2024-01-15) ✓ inserted
[  2/42] draft-post.md → "Draft Post" (draft, skipped)
...
Done. Inserted: 38, Skipped: 3, Errors: 1
```
