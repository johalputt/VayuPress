# vayupress.com

The words of the site served at https://vayupress.com. The install that
serves it follows this repository and builds the site from it on every
change to main (`internal/docsite`), and `cmd/vayudocs` builds the same site
locally. Both the words and the design (`theme/`: templates, stylesheet,
script) live here, so a new look and a new sentence are each a commit. The
binary carries only the engine: it ships to every install, and this site
is not theirs.

```
docs/site/
├── theme/          # the design: templates/*.html, assets/site.css, site.js, updates.js
├── products.json   # the home page's products: name, one line, icon, its decision
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
