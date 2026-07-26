// SPDX-License-Identifier: Apache-2.0

package vayutor

import (
	"bufio"
	"net"
	"strings"
	"testing"
)

// fakeTor answers control commands on one end of a net.Pipe with canned,
// spec-shaped replies, so the control client can be exercised without a real
// tor daemon.
func fakeTor(t *testing.T) *control {
	t.Helper()
	cli, srv := net.Pipe()
	go func() {
		r := bufio.NewReader(srv)
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			switch {
			case strings.HasPrefix(line, "AUTHENTICATE"):
				srv.Write([]byte("250 OK\r\n"))
			case strings.HasPrefix(line, "ADD_ONION NEW:"):
				srv.Write([]byte("250-ServiceID=abcdefghij234567\r\n250-PrivateKey=ED25519-V3:BASE64KEYDATA==\r\n250 OK\r\n"))
			case strings.HasPrefix(line, "ADD_ONION ED25519-V3:"):
				srv.Write([]byte("250-ServiceID=abcdefghij234567\r\n250 OK\r\n"))
			case strings.HasPrefix(line, "DEL_ONION"):
				srv.Write([]byte("250 OK\r\n"))
			default:
				srv.Write([]byte("510 Unrecognized command\r\n"))
			}
		}
	}()
	t.Cleanup(func() { cli.Close(); srv.Close() })
	return newControl(cli)
}

func TestAuthenticate(t *testing.T) {
	c := fakeTor(t)
	if err := c.authenticate([]byte{0x01, 0x02, 0x03}); err != nil {
		t.Fatalf("authenticate: %v", err)
	}
}

func TestAddOnionNewCarriesKey(t *testing.T) {
	c := fakeTor(t)
	on, err := c.addOnion("NEW:ED25519-V3", "127.0.0.1:8080")
	if err != nil {
		t.Fatalf("addOnion: %v", err)
	}
	if on.serviceID != "abcdefghij234567" {
		t.Errorf("serviceID = %q", on.serviceID)
	}
	if on.host != "abcdefghij234567.onion" {
		t.Errorf("host = %q", on.host)
	}
	if on.privateKey != "ED25519-V3:BASE64KEYDATA==" {
		t.Errorf("privateKey = %q (new onion must return a key to persist)", on.privateKey)
	}
}

func TestAddOnionExistingNoKey(t *testing.T) {
	c := fakeTor(t)
	on, err := c.addOnion("ED25519-V3:BASE64KEYDATA==", "127.0.0.1:8080")
	if err != nil {
		t.Fatalf("addOnion: %v", err)
	}
	if on.host != "abcdefghij234567.onion" {
		t.Errorf("host = %q", on.host)
	}
	if on.privateKey != "" {
		t.Errorf("re-attach must not return a key, got %q", on.privateKey)
	}
}

func TestDelOnion(t *testing.T) {
	c := fakeTor(t)
	if err := c.delOnion("abcdefghij234567"); err != nil {
		t.Fatalf("delOnion: %v", err)
	}
}

// TestReplyParsing exercises the multi-line reply parser directly.
func TestReplyParsing(t *testing.T) {
	cli, srv := net.Pipe()
	defer cli.Close()
	defer srv.Close()
	go func() {
		srv.Write([]byte("250-A=1\r\n250-B=two words\r\n250 OK\r\n"))
	}()
	c := newControl(cli)
	rp, err := c.readReply()
	if err != nil {
		t.Fatalf("readReply: %v", err)
	}
	if !rp.ok() {
		t.Fatalf("code = %d", rp.code)
	}
	if rp.value("A") != "1" || rp.value("B") != "two words" {
		t.Errorf("values = %+v", rp.lines)
	}
}

func TestServiceIDOf(t *testing.T) {
	cases := map[string]string{
		"abc.onion":             "abc",
		"http://abc.onion/path": "abc",
		"https://ABC.onion":     "abc",
		"abc.onion:80":          "abc.onion:80", // port not stripped here (caller normalizes)
	}
	for in, want := range cases {
		if got := serviceIDOf(in); got != want {
			t.Errorf("serviceIDOf(%q) = %q, want %q", in, got, want)
		}
	}
}
