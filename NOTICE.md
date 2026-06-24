Apache License 2.0
Copyright (c) 2025 VayuPress Contributors

https://github.com/johalputt/vayupress

This software includes components from the following open source projects:

────────────────────────────────────────────────────────────────────────────────

Mox
Copyright (c) Mechiel Lukkien
MIT License
Source: https://github.com/mjl-/mox
Commit: v0.0.13
Used in: internal/vayuos/mail/core/
Notes: VayuMail is derived from Mox's SMTP/IMAP engine architecture.
       Mox standalone CLI, web admin, and account management have been
       replaced with VayuPress equivalents.

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

────────────────────────────────────────────────────────────────────────────────

ProtonMail go-crypto
Copyright (c) The ProtonMail Authors
Apache License 2.0
Source: https://github.com/ProtonMail/go-crypto
Used in: internal/vayuos/pgp/ (planned integration)
Notes: VayuPGP will use go-crypto for Ed25519 key generation, Curve25519
       encryption, and PGP message parsing. Currently using Go stdlib
       crypto packages; go-crypto will replace stubs in engine.go.
