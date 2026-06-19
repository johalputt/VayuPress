# hugo2vayu

A CLI tool to import Hugo Markdown content into a VayuPress SQLite database.

## Features

- Parses Hugo Markdown files with YAML (`---`) or TOML (`+++`) frontmatter
- Extracts title, slug, tags, categories, date, and draft status
- Merges categories into tags (deduplicated)
- Derives slug from filename if not in frontmatter (strips `YYYY-MM-DD-` date prefix)
- Renders Markdown body to HTML using goldmark (GitHub Flavored Markdown)
- Resume support via checkpoint table (restart after interruption)
- Dry-run mode for previewing without writing

## Installation

```bash
go install github.com/johalputt/hugo2vayu/cmd/hugo2vayu@latest
```

Or build from source:

```bash
git clone ...
cd hugo2vayu
go build -o hugo2vayu ./cmd/hugo2vayu
```

## Usage

### Import

```bash
hugo2vayu import \
  --site /path/to/hugo-site \
  --vayu-db /path/to/vayupress.db \
  --content-dir content \
  --skip-drafts \
  --resume
```

Flags:

| Flag            | Default     | Description                                         |
|-----------------|-------------|-----------------------------------------------------|
| `--site`        | (required)  | Hugo site root directory                            |
| `--vayu-db`     | (required)  | Path to VayuPress SQLite database                   |
| `--content-dir` | `content`   | Content subdirectory within the site root           |
| `--recursive`   | `true`      | Walk content directory recursively                  |
| `--skip-drafts` | `true`      | Skip posts with `draft: true`                       |
| `--dry-run`     | `false`     | Preview without writing to database                 |
| `--resume`      | `true`      | Resume from last checkpoint after interruption      |

Progress output format:
```
[  1/42] my-slug → "My Post Title" (2024-01-15) ✓ inserted
[  2/42] draft-post → skipped (draft)
```

### List

Preview parsed metadata without writing to the database:

```bash
hugo2vayu list --site /path/to/hugo-site
```

## Hugo Frontmatter Support

**YAML:**
```yaml
---
title: "My Post"
slug: my-post
date: 2024-01-15
tags: [go, web]
categories: [programming]
draft: false
---
```

**TOML:**
```toml
+++
title = "My Post"
date = "2024-01-15"
draft = false
tags = ["go", "web"]
+++
```
