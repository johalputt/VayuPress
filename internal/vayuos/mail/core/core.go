// Package core provides the VayuMail SMTP/IMAP engine.
//
// Derived from Mox by Mechiel Lukkien (MIT license).
// Source: https://github.com/mjl-/mox
// Commit: v0.0.13 (latest stable tagged release)
//
// The original Mox is a full-featured modern mail server written in Go.
// VayuMail adopts its core architecture (SMTP listener, IMAP server,
// Maildir storage, DKIM signer) while replacing Mox's standalone web
// admin, CLI, and account management with VayuPress equivalents.
//
// Modifications from upstream Mox:
//   - Removed: main(), CLI tooling, web admin UI, standalone config parser
//   - Removed: own user/account management, own auth system
//   - Kept: SMTP protocol engine, IMAP protocol engine, DKIM signer, message queue
//   - Added: VayuPress bridge integration for auth + storage delegation
//
// Original Mox copyright notice:
//   Copyright (c) Mechiel Lukkien
//   Permission is hereby granted, free of charge, to any person obtaining a copy
//   of this software and associated documentation files (the "Software"), to deal
//   in the Software without restriction...
package core