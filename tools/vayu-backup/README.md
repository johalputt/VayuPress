# vayu-backup

Backup and restore tool for VayuPress SQLite databases.

## Usage

```
vayu-backup backup   --db vayupress.db [--out backup.tar.gz] [--compress]
vayu-backup restore  --backup backup.tar.gz --db vayupress.db [--force]
vayu-backup list     backup.tar.gz
vayu-backup verify   backup.tar.gz
vayu-backup schedule
```

## Build

```
CGO_ENABLED=1 go build -o vayu-backup ./cmd/vayu-backup
```
