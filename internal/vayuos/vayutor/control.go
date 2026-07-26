// SPDX-License-Identifier: Apache-2.0

// Package vayutor is the VayuTor subsystem: it makes every domain VayuPress
// hosts reachable as a Tor v3 onion service, alongside its normal clearnet URL,
// with a single click and no third party able to see who visits. VayuPress never
// runs the Tor network itself; it drives a locally-running tor daemon over its
// authenticated control port (ADD_ONION), persisting each onion's key so the
// .onion address is stable across restarts. Nothing about a visitor is recorded
// beyond an aggregate count. See docs/adr/ADR-0138-vayutor-onion-services.md.
package vayutor

import (
	"bufio"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// controlDialTimeout bounds a single connect/handshake to the control port.
const controlDialTimeout = 5 * time.Second

// control is a minimal Tor control-protocol client (tor-spec/control-spec):
// CRLF commands, replies as one or more status lines ("250-…"/"250 …"). It
// speaks only the handful of verbs VayuTor needs (AUTHENTICATE, ADD_ONION,
// DEL_ONION, GETINFO) and is deliberately dependency-free so it can be unit
// tested against an in-memory fake control port.
type control struct {
	conn net.Conn
	r    *bufio.Reader
}

// dialControl connects to the tor control endpoint. addr is either "host:port"
// (TCP) or a filesystem path (unix socket, when it starts with "/" or "unix:").
func dialControl(addr string) (*control, error) {
	network, address := "tcp", addr
	switch {
	case strings.HasPrefix(addr, "unix:"):
		network, address = "unix", strings.TrimPrefix(addr, "unix:")
	case strings.HasPrefix(addr, "/"):
		network = "unix"
	}
	conn, err := net.DialTimeout(network, address, controlDialTimeout)
	if err != nil {
		return nil, err
	}
	return newControl(conn), nil
}

// newControl wraps an established connection (used by dialControl and tests).
func newControl(conn net.Conn) *control {
	return &control{conn: conn, r: bufio.NewReader(conn)}
}

// Close ends the control session. Because VayuTor's onions are created WITHOUT
// the Detach flag, closing the connection tells tor to tear them down — the
// onion's lifetime is bound to VayuPress being up, which is exactly the property
// we want.
func (c *control) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// reply is a parsed control reply: the final status code plus every data line
// (the "Key=Value" payloads carried on 250-/250+ lines, in order).
type reply struct {
	code  int
	lines []string
}

// ok reports a 2xx success.
func (rp reply) ok() bool { return rp.code >= 200 && rp.code < 300 }

// value returns the value of the first "Key=Value" data line with the given key.
func (rp reply) value(key string) string {
	pfx := key + "="
	for _, ln := range rp.lines {
		if strings.HasPrefix(ln, pfx) {
			return strings.TrimSpace(ln[len(pfx):])
		}
	}
	return ""
}

// send writes one command and reads its complete reply. A short deadline keeps a
// wedged daemon from hanging the caller (onion creation is fast; a stall means
// something is wrong).
func (c *control) send(cmd string) (reply, error) {
	_ = c.conn.SetDeadline(time.Now().Add(controlDialTimeout))
	defer func() { _ = c.conn.SetDeadline(time.Time{}) }()
	if _, err := c.conn.Write([]byte(cmd + "\r\n")); err != nil {
		return reply{}, err
	}
	return c.readReply()
}

// readReply consumes one reply. Each line begins with a 3-digit status code; a
// space after the code marks the LAST line, a '-' (or '+') marks a continuation.
// '+' introduces a dot-terminated data block, which we read through to its "."
// line. The final line's code is authoritative.
func (c *control) readReply() (reply, error) {
	var rp reply
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			return rp, err
		}
		line = strings.TrimRight(line, "\r\n")
		if len(line) < 4 {
			return rp, fmt.Errorf("vayutor: malformed control line %q", line)
		}
		code, sep, payload := line[:3], line[3], line[4:]
		n := 0
		for _, ch := range code {
			if ch < '0' || ch > '9' {
				return rp, fmt.Errorf("vayutor: bad status code %q", code)
			}
			n = n*10 + int(ch-'0')
		}
		rp.code = n
		if payload != "" {
			rp.lines = append(rp.lines, payload)
		}
		switch sep {
		case ' ':
			return rp, nil // final line
		case '+':
			// Read the multi-line data block up to a lone ".".
			for {
				d, err := c.r.ReadString('\n')
				if err != nil {
					return rp, err
				}
				d = strings.TrimRight(d, "\r\n")
				if d == "." {
					break
				}
				rp.lines = append(rp.lines, d)
			}
		case '-':
			// continuation; keep reading
		default:
			return rp, fmt.Errorf("vayutor: bad separator %q", string(sep))
		}
	}
}

// authenticate performs cookie authentication: the raw bytes of the control auth
// cookie are hex-encoded and presented to AUTHENTICATE. An empty cookie sends a
// password-less AUTHENTICATE (only valid when the daemon has no auth configured).
func (c *control) authenticate(cookie []byte) error {
	arg := ""
	if len(cookie) > 0 {
		arg = " " + hex.EncodeToString(cookie)
	}
	rp, err := c.send("AUTHENTICATE" + arg)
	if err != nil {
		return err
	}
	if !rp.ok() {
		return fmt.Errorf("vayutor: authentication rejected (%d)", rp.code)
	}
	return nil
}

// getInfo issues GETINFO for a single key and returns its value (e.g.
// "status/bootstrap-phase" → "NOTICE BOOTSTRAP PROGRESS=100 TAG=done …").
func (c *control) getInfo(key string) (string, error) {
	rp, err := c.send("GETINFO " + key)
	if err != nil {
		return "", err
	}
	if !rp.ok() {
		return "", fmt.Errorf("vayutor: GETINFO %s failed (%d)", key, rp.code)
	}
	return rp.value(key), nil
}

// onion is a created onion service: its address (without the ".onion" suffix as
// tor reports the ServiceID, plus the full host) and, when freshly minted, the
// private key to persist for a stable address next time.
type onion struct {
	serviceID  string // e.g. "abcd…" (56 chars, no scheme/suffix)
	host       string // serviceID + ".onion"
	privateKey string // "ED25519-V3:<base64>" — only set when newly generated
}

// addOnion creates (or re-attaches) a v3 onion mapping virtual port 80 to the
// given local target ("127.0.0.1:8080"). keyBlob is "NEW:ED25519-V3" to mint a
// fresh identity (the reply then carries the PrivateKey to persist) or a stored
// "ED25519-V3:<base64>" to bring the SAME .onion back up. No Detach flag: the
// onion is torn down if this control connection drops.
func (c *control) addOnion(keyBlob, target string) (onion, error) {
	cmd := fmt.Sprintf("ADD_ONION %s Port=80,%s", keyBlob, target)
	rp, err := c.send(cmd)
	if err != nil {
		return onion{}, err
	}
	if !rp.ok() {
		return onion{}, fmt.Errorf("vayutor: ADD_ONION failed (%d)", rp.code)
	}
	id := rp.value("ServiceID")
	if id == "" {
		return onion{}, errors.New("vayutor: ADD_ONION returned no ServiceID")
	}
	return onion{serviceID: id, host: id + ".onion", privateKey: rp.value("PrivateKey")}, nil
}

// delOnion removes a running onion by its ServiceID (the address without the
// ".onion" suffix). A "552 unknown service" is treated as already-gone.
func (c *control) delOnion(serviceID string) error {
	rp, err := c.send("DEL_ONION " + serviceID)
	if err != nil {
		return err
	}
	if !rp.ok() && rp.code != 552 {
		return fmt.Errorf("vayutor: DEL_ONION failed (%d)", rp.code)
	}
	return nil
}

// serviceIDOf strips a scheme and the ".onion" suffix from a host, yielding the
// bare ServiceID tor uses in DEL_ONION.
func serviceIDOf(host string) string {
	h := strings.TrimPrefix(strings.TrimPrefix(host, "http://"), "https://")
	h = strings.ToLower(strings.TrimSpace(h))
	if i := strings.IndexByte(h, '/'); i >= 0 {
		h = h[:i] // drop any path before stripping the suffix
	}
	return strings.TrimSuffix(h, ".onion")
}
