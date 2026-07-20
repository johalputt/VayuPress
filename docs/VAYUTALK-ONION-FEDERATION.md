# VayuTalk onion-to-onion delivery (operator guide)

Experimental (ADR-0142). Lets two people, each running their own VayuPress **Tor
install**, exchange ephemeral end-to-end-encrypted VayuTalk messages using each
other's anonymous codes (`anon…@<onion>`). It only exists in the Tor world
(`VAYUOS_MODE=tor`); it has no effect on clearnet or same-install VayuTalk.

It is **off by default** and takes two deliberate steps to turn on.

## Enabling it

1. **Switch on federation.** In the Tor-world console, open **VayuTalk** and click
   **Enable onion-to-onion** (or set `talk.onion_federation=on`). This opens the
   inbound delivery endpoint so other onions can reach your code, and lets your
   install attempt outbound delivery.

2. **Point the install at a Tor SOCKS proxy.** Outbound delivery rides Tor. Set:

   ```sh
   VAYUOS_TOR_SOCKS_ADDR=127.0.0.1:9050
   ```

   to your tor's SOCKS address (a system tor's default is `127.0.0.1:9050`). With
   this unset, **nothing is ever dialled** — you can still be reached, but your
   install will report that no Tor SOCKS proxy is configured when you try to send.

Restart the service after changing the environment variable.

## How it works

- **Inbound.** Peers fetch your code's public key from your onion's WKD and POST a
  ciphertext envelope to `POST /api/v1/talk/onion/deliver` on your onion. Your
  install enqueues it in the local relay, so your open VayuTalk stream shows it.
- **Outbound.** When you message a code on a different `.onion`, your install
  fetches that peer's key from **their** onion over Tor, encrypts+signs locally,
  and POSTs the envelope to their delivery endpoint over Tor.

## Security model

- **Onion-only outbound.** The outbound HTTP client routes solely through the Tor
  SOCKS proxy and **refuses to dial any host that is not a `.onion`** — the check
  runs before any connection, never resolves DNS locally, and never follows
  redirects. Clearnet is unreachable from this lane by construction.
- **Closed inbound.** The delivery endpoint returns 404 unless federation is on,
  accepts envelopes **only** addressed to your own current code (never an open
  relay), requires a well-formed onion sender, and is bounded by the same 64 KiB
  ciphertext cap, per-recipient/global queue caps, and rate limit as every other
  message. Payloads are opaque ciphertext and are never decrypted at the edge.
- **End to end.** Messages are encrypted to the recipient's key and signed by the
  sender, exactly like same-install VayuTalk. Verify safety numbers out of band.

## Limits (experimental)

- Rotating your code changes the address peers must use; hand out the new one.
- There is no cross-onion read receipt yet — the sender does not learn when the
  recipient reads a message delivered to another onion.
- Inbound sender-signature verification and a managed-tor SOCKS convenience
  (auto-opening a loopback SOCKS port) are planned follow-ups.
- **Validate on real onions.** This path cannot be exercised in CI; confirm
  delivery between two live `.onion` installs before relying on it.
