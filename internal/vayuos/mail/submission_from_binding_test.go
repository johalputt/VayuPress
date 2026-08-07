// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"net"
	"strings"
	"testing"
	"time"
)

// SECTION 2 AUDIT FINDING, in the attacker's voice:
//
//	I have a mailbox on your server. An ordinary one — I am not an
//	administrator, I have no API key, I did nothing but get hired.
//
//	Your envelope binding works. I cannot say MAIL FROM:<ceo@example.com>;
//	submissionSenderAllowed refuses it with 553. So I do not.
//
//	  MAIL FROM:<alice@example.com>      <- mine. Accepted.
//	  RCPT TO:<board@partner.example>
//	  DATA
//	  From: ceo@example.com              <- the only address the recipient sees.
//
//	Nothing looks at that header. relayOutbound takes the message verbatim,
//	picks the signing key from the ENVELOPE sender — example.com — and DKIM-signs
//	it. So the message leaves with d=example.com, the From header says
//	example.com, and the two are perfectly aligned.
//
//	The recipient's server checks DMARC and it PASSES. Your signature is the
//	thing vouching for me. This is not ordinary spoofing, which lands in junk —
//	it is a mail from the CEO with your cryptographic word behind it.
//
// The envelope guard was added for exactly this threat and stops one field
// short. RFC 5322's From is what every mail client renders; the envelope is
// invisible to the reader.
//
// headerFromDomain already exists in this package, correct and hardened, with a
// long comment about how attackers split verifier and renderer apart. It is
// called on INBOUND mail only. Submission never asks.

// submissionHarness starts a real submission server with a sender-binding
// predicate and returns an authenticated, TLS-wrapped session ready for MAIL.
// Driving the actual protocol matters here: the defect is in what the server
// does with DATA, and a unit test on the predicate cannot see DATA at all —
// which is precisely why the existing predicate tests all pass while this
// hole is open.
func submissionHarness(t *testing.T, relay func(from string, rcpts []string, raw []byte) error) (*tls.Conn, *bufio.Reader) {
	t.Helper()
	cfg := testEngineConfig()

	auth := func(u, p string) (bool, error) { return u == "alice@example.com" && p == "pw", nil }
	// Mirrors submissionSenderAllowed, INCLUDING its fail-open on an empty
	// address — and that detail is the whole reason this comment is long.
	//
	// The first version of this harness returned false for an empty address,
	// i.e. it was STRICTER than the shipped predicate. Every test still passed,
	// and the mutation that deleted the well-formed check survived: the malformed
	// cases were being refused by the ownership branch, which only refused them
	// because this harness was stricter than the product. Against the real
	// predicate — which returns true when fromAddr is "" so that a check it
	// cannot evaluate never blocks delivery — deleting the well-formed check
	// would let every malformed From through.
	//
	// A harness that differs from the product in EITHER direction proves nothing
	// about the product. Stricter hides missing controls; looser invents them.
	senderOK := func(authUser, from string) bool {
		authUser, from = strings.TrimSpace(authUser), strings.TrimSpace(from)
		if authUser == "" || from == "" {
			return true // fail open, exactly as submissionSenderAllowed does
		}
		return strings.EqualFold(authUser, from)
	}

	srv := NewSubmissionServer(cfg, testTLSConfig(t), auth, relay).WithSenderCheck(senderOK)
	if err := srv.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { srv.Stop(context.Background()) })

	conn, err := net.DialTimeout("tcp", srv.Addr(), 3*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	br := bufio.NewReader(conn)
	readUntilFinal(t, br)
	if _, err := conn.Write([]byte("EHLO client\r\n")); err != nil {
		t.Fatalf("ehlo: %v", err)
	}
	readUntilFinal(t, br)
	conn.Write([]byte("STARTTLS\r\n"))
	readUntilFinal(t, br)

	tconn := tls.Client(conn, &tls.Config{InsecureSkipVerify: true, ServerName: "mail.test"})
	if err := tconn.Handshake(); err != nil {
		t.Fatalf("handshake: %v", err)
	}
	tbr := bufio.NewReader(tconn)
	tconn.Write([]byte("EHLO client\r\n"))
	readUntilFinal(t, tbr)

	cred := base64.StdEncoding.EncodeToString([]byte("\x00alice@example.com\x00pw"))
	tconn.Write([]byte("AUTH PLAIN " + cred + "\r\n"))
	if r := readUntilFinal(t, tbr); !strings.HasPrefix(r, "235") {
		t.Fatalf("AUTH failed: %q", r)
	}
	return tconn, tbr
}

// submit runs one full transaction and returns the server's reply to the final
// dot, plus whatever the relay captured.
func submit(t *testing.T, tconn *tls.Conn, tbr *bufio.Reader, envelopeFrom, rcpt, message string) string {
	t.Helper()
	tconn.Write([]byte("MAIL FROM:<" + envelopeFrom + ">\r\n"))
	if r := readUntilFinal(t, tbr); !strings.HasPrefix(r, "250") {
		return r // refused at MAIL — the caller asserts on this
	}
	tconn.Write([]byte("RCPT TO:<" + rcpt + ">\r\n"))
	if r := readUntilFinal(t, tbr); !strings.HasPrefix(r, "250") {
		return r
	}
	tconn.Write([]byte("DATA\r\n"))
	readUntilFinal(t, tbr) // 354
	tconn.Write([]byte(message + "\r\n.\r\n"))
	return readUntilFinal(t, tbr)
}

// THE FINDING.
func TestAuthenticatedSubmitterCannotForgeAnotherMailboxInTheFromHeader(t *testing.T) {
	t.Parallel()
	relayed := make(chan []byte, 1)
	tconn, tbr := submissionHarness(t, func(_ string, _ []string, raw []byte) error {
		relayed <- raw
		return nil
	})

	// Envelope sender is genuinely mine, so the envelope binding is satisfied and
	// never fires. Everything hostile is in the header.
	resp := submit(t, tconn, tbr,
		"alice@example.com", "board@partner.example",
		"From: ceo@example.com\r\n"+
			"To: board@partner.example\r\n"+
			"Subject: Wire transfer approved\r\n"+
			"\r\n"+
			"Please action today.")

	if strings.HasPrefix(resp, "250") {
		t.Errorf("the server accepted a message whose From header names another mailbox "+
			"on this install, and answered %q.\n\n"+
			"It will now DKIM-sign it with the example.com key chosen from the ENVELOPE "+
			"sender, so the message leaves with d=example.com aligned to "+
			"From: ceo@example.com and passes DMARC at the recipient. This server's own "+
			"signature is what makes the forgery credible — ordinary spoofing lands in "+
			"junk, this does not.\n\n"+
			"The envelope binding that refuses MAIL FROM:<ceo@example.com> stops one "+
			"field short of the only address a human ever sees.", resp)
	}

	select {
	case raw := <-relayed:
		t.Fatalf("the forged message reached the relay queue.\n\nThe status code is not "+
			"the control — what matters is that nothing was enqueued.\n\nrelayed:\n%s", raw)
	case <-time.After(300 * time.Millisecond):
		// Nothing relayed. Correct.
	}
}

// The same forgery with only a display name is the one people actually fall for,
// and it must NOT be refused — "Alice (CEO) <alice@example.com>" is a real
// address with a chosen label, which is every mail client's normal behaviour.
// Refusing it would make the guard unusable.
func TestADisplayNameIsStillAllowed(t *testing.T) {
	t.Parallel()
	relayed := make(chan []byte, 1)
	tconn, tbr := submissionHarness(t, func(_ string, _ []string, raw []byte) error {
		relayed <- raw
		return nil
	})

	resp := submit(t, tconn, tbr,
		"alice@example.com", "friend@partner.example",
		"From: \"Alice, Chief Executive\" <alice@example.com>\r\n"+
			"Subject: hello\r\n\r\nbody")

	if !strings.HasPrefix(resp, "250") {
		t.Fatalf("a message from the submitter's OWN address with a display name was "+
			"refused (%q).\n\nDisplay names are ordinary; a guard that rejects them "+
			"breaks every mail client on the install.", resp)
	}
	select {
	case <-relayed:
	case <-time.After(2 * time.Second):
		t.Fatal("a legitimate message was not relayed")
	}
}

// A plain, exactly-matching From must work — the base case, and the one that
// proves the guard is not simply refusing everything.
func TestOwnAddressInFromIsAccepted(t *testing.T) {
	t.Parallel()
	relayed := make(chan []byte, 1)
	tconn, tbr := submissionHarness(t, func(_ string, _ []string, raw []byte) error {
		relayed <- raw
		return nil
	})

	resp := submit(t, tconn, tbr,
		"alice@example.com", "friend@partner.example",
		"From: alice@example.com\r\nSubject: hello\r\n\r\nbody")

	if !strings.HasPrefix(resp, "250") {
		t.Fatalf("the submitter's own address in From was refused: %q", resp)
	}
	select {
	case <-relayed:
	case <-time.After(2 * time.Second):
		t.Fatal("a legitimate message was not relayed")
	}
}

// A message with NO From header at all must not become a bypass. Mail clients
// always send one; a submission that omits it is either broken or probing.
func TestAMissingFromHeaderIsNotABypass(t *testing.T) {
	t.Parallel()
	relayed := make(chan []byte, 1)
	tconn, tbr := submissionHarness(t, func(_ string, _ []string, raw []byte) error {
		relayed <- raw
		return nil
	})

	resp := submit(t, tconn, tbr,
		"alice@example.com", "friend@partner.example",
		"Subject: no from header\r\n\r\nbody")

	// Either outcome is defensible — stamp the authenticated identity, or refuse.
	// What is NOT defensible is relaying a message with no From while the guard
	// believes it checked one.
	if strings.HasPrefix(resp, "250") {
		select {
		case raw := <-relayed:
			if !strings.Contains(strings.ToLower(string(raw)), "from:") {
				t.Error("a message with no From header was relayed as-is.\n\n" +
					"Downstream this renders with whatever the receiving client invents, and " +
					"the guard's whole premise — that the From header names the authenticated " +
					"submitter — is unenforced for this message.")
			}
		case <-time.After(2 * time.Second):
			t.Error("accepted with 250 but nothing was relayed")
		}
	}
}

// The multi-address and duplicate-From shapes headerFromDomain already refuses
// on the inbound side. They are the same trick pointed outward: a verifier reads
// one address, the renderer shows another.
func TestSplitFromHeadersAreRefusedOnSubmission(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, from string }{
		{"two From headers", "From: alice@example.com\r\nFrom: ceo@example.com\r\n"},
		{"two addresses in one From", "From: alice@example.com, ceo@example.com\r\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			relayed := make(chan []byte, 1)
			tconn, tbr := submissionHarness(t, func(_ string, _ []string, raw []byte) error {
				relayed <- raw
				return nil
			})

			resp := submit(t, tconn, tbr,
				"alice@example.com", "board@partner.example",
				tc.from+"Subject: hello\r\n\r\nbody")

			if strings.HasPrefix(resp, "250") {
				select {
				case raw := <-relayed:
					t.Errorf("a %s was accepted and relayed.\n\nThis is the split the "+
						"headerFromDomain comment describes, aimed outward: whichever address "+
						"the recipient's client renders, this server signed it.\n\nrelayed:\n%s",
						tc.name, raw)
				case <-time.After(300 * time.Millisecond):
					t.Errorf("accepted with 250 (%q) but nothing relayed", resp)
				}
			}
		})
	}
}

// WIRING. The two tests above drive a server this file constructed, so they
// prove the check works — not that the shipped one has it. engine.go builds the
// submission server with WithSenderCheck(e.submissionSenderAllowed); if the
// header binding is not attached there, every assertion above is true of a
// server nobody runs.
//
// This is the "a claim is not a control" shape this repo keeps paying for, so it
// gets its own test rather than a comment.
func TestTheShippedSubmissionServerCarriesTheHeaderBinding(t *testing.T) {
	t.Parallel()
	srv := NewSubmissionServer(testEngineConfig(), nil,
		func(string, string) (bool, error) { return true, nil },
		func(string, []string, []byte) error { return nil },
	).WithSenderCheck(func(authUser, from string) bool {
		return strings.EqualFold(authUser, from)
	})

	if srv.headerFromAllowed == nil {
		t.Fatal("WithSenderCheck hardened the envelope and left the From header unchecked.\n\n" +
			"That is the defect this file exists for: the envelope is invisible to a reader, " +
			"the header is the only address they see.")
	}
	if srv.headerFromAllowed("alice@example.com", "ceo@example.com") {
		t.Error("the wired header predicate permits another mailbox's address")
	}
	if !srv.headerFromAllowed("alice@example.com", "alice@example.com") {
		t.Error("the wired header predicate refuses the submitter's own address")
	}
}

// The INBOUND listener must NOT get the header binding. Mail arriving from the
// internet legitimately carries any From in the world, and applying a
// submitter rule there would reject essentially all incoming mail — an outage
// dressed as hardening.
func TestTheInboundListenerDoesNotBindTheFromHeader(t *testing.T) {
	t.Parallel()
	srv := NewSMTPServer(testEngineConfig(),
		func(string, []string, []byte) error { return nil })
	if srv.headerFromAllowed != nil {
		t.Fatal("the inbound listener carries a From-header binding.\n\n" +
			"Every message from the outside world would be refused: the whole point of " +
			"inbound mail is that From names somebody else.")
	}
	if srv.submission {
		t.Fatal("the inbound listener is marked as a submission server")
	}
}
