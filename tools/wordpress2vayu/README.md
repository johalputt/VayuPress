# wordpress2vayu

Migrate WordPress posts to a VayuPress SQLite database.

## Build

```sh
go build -o wp2vayu ./cmd/wp2vayu
```

## Usage

### Count posts

```sh
./wp2vayu count \
  --wp-dsn "user:pass@tcp(localhost:3306)/wordpress" \
  --status publish \
  --post-type post
```

### Migrate posts

```sh
./wp2vayu migrate \
  --wp-dsn "user:pass@tcp(localhost:3306)/wordpress" \
  --vayu-db vayu.db \
  --status publish \
  --post-type post \
  --batch 50 \
  --delay 200ms
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--wp-dsn` | (required) | WordPress MySQL DSN |
| `--vayu-db` | `vayu.db` | VayuPress SQLite database path |
| `--status` | `publish` | Post status: `publish`, `draft`, `all` |
| `--post-type` | `post` | Post type: `post`, `page`, `both` |
| `--table-prefix` | `wp_` | WordPress table prefix |
| `--batch` | `50` | Posts per batch |
| `--delay` | `200ms` | Delay between batches |
| `--resume` | `true` | Resume from last checkpoint |
| `--dry-run` | `false` | Simulate without writing |

## Resume

If a migration is interrupted (Ctrl+C), it saves a checkpoint. Re-run with `--resume` (default) to continue from where it stopped.
