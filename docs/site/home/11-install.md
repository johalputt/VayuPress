---
kicker: Ship in minutes
---
## One command to a live server.

On a server with a domain pointed at it, one command installs everything:

```
curl -sSL https://raw.githubusercontent.com/johalputt/vayupress/main/scripts/deploy-vayupress.sh | bash
```

Or build it from the source and run it on your own machine with the settings in the [development guide](https://github.com/johalputt/VayuPress/blob/main/docs/DEVELOPMENT.md):

```
git clone https://github.com/johalputt/vayupress && cd vayupress
CGO_ENABLED=1 go build ./cmd/vayupress
CGO_ENABLED=1 go test ./...
```

The installer provisions nginx and Let's Encrypt certificates for your website, `blog.` and `mail.` hosts. To add the real-time VayuTalk relay, point one more record (`talk.yourdomain.com` to your server, with no CDN proxy in front) and run the installer again; it adds the certificate, the host and the app's advertisement. Every step is in the [installation guide](https://github.com/johalputt/VayuPress/blob/main/docs/INSTALLATION.md).
