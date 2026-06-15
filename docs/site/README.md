# VayuPress marketing site

Source for the GitHub Pages site served at https://vayupress.com
(deployed from the `gh-pages` branch root).

## Structure

```
docs/site/
├── index.html        # single-page site (Tailwind CDN + Alpine.js)
├── CNAME             # custom domain: vayupress.com
├── assets/
│   ├── style.css     # premium styles (wind, tilt, marquee, reveal)
│   ├── app.js        # wind canvas, cursor aura, magnetic hover, Alpine root
│   ├── mark.png      # V + wind mark (light, for dark bg)
│   ├── wordmark.png  # full logo (light)
│   └── favicon-*.png
└── screenshots/      # copied from docs/screenshots on deploy
```

## Deploy

The `gh-pages` branch mirrors this folder at its root (plus `screenshots/`).
A push to `gh-pages` triggers the GitHub Pages build automatically.
