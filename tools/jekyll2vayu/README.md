# jekyll2vayu

A CLI tool to import Jekyll Markdown posts into a VayuPress SQLite database.

## Overview

`jekyll2vayu` walks a Jekyll site's `_posts/` directory, parses each Markdown file (including YAML frontmatter), and inserts the resulting articles into a VayuPress SQLite database.

Features:
- Parses Jekyll post filenames (`YYYY-MM-DD-slug.md`) for date and slug
- Reads YAML frontmatter for title, date (override), tags, categories, and published status
- Merges categories into tags (deduplicated)
- Converts Markdown body to HTML using goldmark with GFM extensions
- Resume support via checkpoint table (skips already-processed files on re-run)
- Dry-run mode for previewing without writing

## Installation

```bash
go install github.com/johalputt/jekyll2vayu/cmd/jekyll2vayu@latest
```

## Usage

### Import posts

```bash
jekyll2vayu import \
  --site /path/to/jekyll-site \
  --vayu-db /path/to/vayupress.db \
  --posts-dir _posts \
  --skip-drafts \
  --resume
```

### List posts (no DB write)

```bash
jekyll2vayu list \
  --site /path/to/jekyll-site \
  --posts-dir _posts
```

### Dry run

```bash
jekyll2vayu import \
  --site /path/to/jekyll-site \
  --vayu-db /path/to/vayupress.db \
  --dry-run
```

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--site` | (required) | Jekyll site root directory |
| `--vayu-db` | (required, import only) | Path to VayuPress SQLite database |
| `--posts-dir` | `_posts` | Posts directory relative to site root |
| `--skip-drafts` | `true` | Skip posts with `published: false` |
| `--dry-run` | `false` | Parse and print without writing to DB |
| `--resume` | `true` | Resume from last checkpoint |

## Jekyll file format

Posts should follow the standard Jekyll naming convention:

```
_posts/YYYY-MM-DD-slug-title.md
```

Each file may contain YAML frontmatter:

```yaml
---
title: My Post Title
date: 2024-01-15
tags:
  - go
  - tutorial
categories:
  - programming
published: true
---

Post body in Markdown...
```
