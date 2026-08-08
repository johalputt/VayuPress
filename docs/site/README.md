# VayuPress marketing site

Source for the site served at https://vayupress.com.

It is **no longer published to GitHub Pages.** vayupress.com is served from a
VayuPress install, as an uploaded custom website, and this folder is the source
that bundle is built from. `.github/workflows/deploy-site.yml` and the `CNAME`
that carried the Pages custom domain are both gone.

## Structure

```
docs/site/
├── index.html        # single-page site (Tailwind + Alpine)
├── .well-known/
│   └── security.txt  # RFC 9116 contact — see "The expiry date is live" below
├── assets/
│   ├── style.css     # premium styles (wind, tilt, marquee, reveal)
│   ├── app.js        # aurora canvas, cursor aura, magnetic hover, Alpine root
│   ├── mark.png      # V + wind mark (light, for dark bg)
│   ├── wordmark.png  # full logo (light)
│   └── favicon-*.png
└── (screenshots/)    # NOT in this folder — see below
```

The gallery images live in `docs/screenshots/`, outside this folder.
`scripts/build-selfhosted-site.sh` copies them into the bundle; nothing else
does, which is why 31 broken images once shipped in every upload.

## Building the bundle

```bash
bash scripts/build-selfhosted-site.sh          # → dist/selfhosted-site/
```

The script rewrites the CDN tags in `index.html` to same-origin assets, because
an install refuses off-origin subresources, `<style>` blocks and inline scripts.
**Do not hand-edit those CDN tags in the source** — the script regex-matches
them.

`tag-release.yml` runs the same script and attaches the zip to every release.

## The expiry date is live

`.well-known/security.txt` carries an RFC 9116 `Expires` field. It used to be
stamped at publish time by the daily Pages workflow, so the committed value was
a placeholder that never mattered. It matters now: the file ships verbatim in
the bundle, and nothing rewrites it.

`scripts/check-security-txt.py` runs in CI and fails once the file is within 45
days of expiring. When it fires, push the date out. An expired security.txt is
worse than none — scanners report it as the finding it exists to prevent.

## Deploy

VayuOS → the domain → Website → upload `dist/selfhosted-site/` as a zip.

The bundle needs the per-domain **eval opt-in** (VayuOS → the domain → Website →
"Allow this site to use eval"): `index.html` drives its behaviour through ~250
Alpine expression strings, which Alpine's standard build compiles with
`new Function()`. Without the opt-in the layout and typography are correct and
the animations are inert.
