# vayupress.com

The words of the site served at https://vayupress.com. The install that
serves it follows this repository and builds the site from it on every
change to main (`internal/docsite`); `cmd/vayudocs` builds the same site
locally, and each release attaches it as `vayupress-selfhosted-site.zip`.
The words live here; the design (templates, stylesheet, script) ships with
the binary, so a new look is a release and a new sentence is a commit.

```
docs/site/
├── nav.json        # the docs sidebar: its headings, and the guides under each
├── home/           # the home page's sections, in file-name order
├── pages/          # the site's own pages: About, Sponsors, VayuWeb, VayuMail privacy
├── img/            # the console pictures the home page shows
└── public/         # served as it is, at the site's root
    ├── .well-known/security.txt   # RFC 9116; its Expires date is checked in CI
    ├── <key>.txt                  # the IndexNow key
    ├── webx.html                  # the old WebX address, sent on to VayuWeb
    └── assets/logo-dark.png       # the brand's mark, which the console's is cut from
```

A section or a page is Markdown with a front matter block (`kicker:`, and
for a page `path:`, `title:` and `description:`). Either may write
`{decisions}` and `{releases}` and gets the build's own counts. Every guide
in `docs/` and every decision in `docs/adr/` is published without being
listed here.

Build it locally:

```
go run ./cmd/vayudocs -repo . -commit "$(git rev-parse HEAD)" -out dist/site
```
