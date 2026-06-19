# vayu-export

Export VayuPress articles to a static HTML site.

## Usage

```
vayu-export export --db vayupress.db [--out ./vayu-site] [--base-url https://example.com] [--page-size 20] [--clean]
vayu-export count  --db vayupress.db
```

## Build

```
CGO_ENABLED=1 go build -o vayu-export ./cmd/vayu-export
```
