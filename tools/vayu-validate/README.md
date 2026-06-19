# vayu-validate

Content integrity checker for VayuPress SQLite databases.

## What it checks

| Rule | Severity | Description |
|------|----------|-------------|
| `empty-title` | ERROR | Article has no title |
| `empty-slug` | ERROR | Article has no slug |
| `invalid-slug` | ERROR | Slug contains uppercase, spaces, or special characters |
| `duplicate-slug` | ERROR | Two articles share the same slug |
| `empty-content` | ERROR | Article body is empty |
| `invalid-created-at` | ERROR | `created_at` cannot be parsed as a date |
| `invalid-updated-at` | ERROR | `updated_at` cannot be parsed as a date |
| `suspicious-date` | WARNING | Date is before year 2000 (likely a bad migration) |
| `oversized-content` | WARNING | Content exceeds 5 MB |
| `invalid-tag` | WARNING | Tag contains characters that may not render correctly |

Exits with code **1** if any ERROR is found (useful in CI pipelines).

## Installation

```bash
cd tools/vayu-validate
CGO_ENABLED=1 go build -o vayu-validate ./cmd/vayu-validate
```

## Usage

```bash
# Validate the database
./vayu-validate validate --db /var/lib/vayupress/vayupress.db

# Content statistics
./vayu-validate stats --db /var/lib/vayupress/vayupress.db
```

### Example output

```
→ Validating vayupress.db…

  ✗ [invalid-slug]  My Post Title: slug "My Post Title" contains invalid characters
  ⚠ [suspicious-date]  old-post: created_at "0001-01-01T00:00:00Z" is before year 2000

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  Articles : 312
  Errors   : 1
  Warnings : 1
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
```

## CI integration

```bash
./vayu-validate validate --db vayupress.db || echo "Database has integrity issues"
```
