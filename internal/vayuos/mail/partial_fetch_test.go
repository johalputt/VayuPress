// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"math"
	"strconv"
	"testing"
)

// SECTION 2 AUDIT FINDING — the whole install, killed by one IMAP command.
//
// In the attacker's voice:
//
//	I have a mailbox. Any mailbox. I sign in to IMAP, select a folder, and send
//	one line:
//
//	    a1 UID FETCH 1 BODY[]<0.-1>
//
//	RFC 3501's <offset.size> partial is two numbers, and applyPartialFetch reads
//	them with Sscanf("%d.%d"). It rejects a negative OFFSET. It never looks at
//	the size. So `end := off + size` goes negative, the clamp below only ever
//	pulls end DOWN, and `b[off:end]` runs with end < off — slice bounds out of
//	range.
//
//	There is no recover() anywhere in this package. An unrecovered panic in any
//	goroutine takes the process with it, and this is the single binary: the
//	website, the blog, the admin console, SMTP receive, submission, POP3 and the
//	database writer all stop at once. I can send it again the moment it comes
//	back, so it is not a crash — it is an off switch.
//
// Two ways in, and the second is the one a size check alone would miss:
// `<1.9223372036854775807>` overflows off+size to a negative number, so the
// arithmetic produces end < off without any negative input.

// partialSpecs are the shapes that must never panic. Each is a legal-looking
// FETCH partial that a client can send.
func TestAPartialFetchSpecCannotPanicTheProcess(t *testing.T) {
	body := []byte("Subject: hello\r\n\r\nthe body of the message")

	for _, spec := range []string{
		"0.-1",                           // the finding: negative size
		"5.-3",                           // negative size with a real offset
		"-1.-1",                          // both negative
		"0." + strconv.Itoa(math.MaxInt), // huge size
		"1." + strconv.Itoa(math.MaxInt), // off+size overflows to negative
		"9999999999999999999.1",          // offset that will not fit an int
		"0.99999999999999999999",         // size that will not fit an int
		".",                              // degenerate
		"..",                             //
		"-",                              //
		"0.",                             //
		".5",                             //
	} {
		t.Run(spec, func(t *testing.T) {
			// A panic here is the finding. Recovering makes the failure legible
			// instead of taking the test binary down with the same bug.
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("applyPartialFetch panicked on BODY[]<%s>: %v\n\n"+
						"There is no recover() in this package, so in the running server this "+
						"panic is on the IMAP connection goroutine and it terminates the whole "+
						"process — website, admin console, SMTP, POP3 and the database writer "+
						"together. Any mailbox holder can send it, and send it again.", spec, r)
				}
			}()
			out, _, _ := applyPartialFetch(body, spec)
			if len(out) > len(body) {
				t.Errorf("returned %d bytes from a %d-byte body", len(out), len(body))
			}
		})
	}
}

// Not panicking is not enough: a malformed spec must also not be answered with
// MORE data than it asked for.
//
// This test exists because a mutation exposed the first version as too weak.
// Deleting the `size < 0` guard still passed, since the overflow guard catches
// the same slice — but the two disagree on what comes back. Without the size
// check, BODY[]<0.-1> clamps end to len(b) and returns THE WHOLE REST OF THE
// BODY for a request whose stated length is negative. Silently over-serving a
// malformed read is the wrong direction to fail in, and "it did not crash" could
// not tell the two apart.
func TestANegativeSizeReturnsNothingRatherThanTheRestOfTheBody(t *testing.T) {
	body := []byte("0123456789")
	for _, spec := range []string{"0.-1", "3.-5"} {
		got, _, partial := applyPartialFetch(body, spec)
		if len(got) != 0 {
			t.Errorf("BODY[]<%s> returned %q (%d bytes).\n\n"+
				"The client asked for a negative number of octets and was handed the rest of "+
				"the message. A malformed partial must yield nothing, not more.",
				spec, got, len(got))
		}
		if !partial {
			t.Errorf("BODY[]<%s> did not report itself as a partial fetch", spec)
		}
	}
}

// The control. A partial fetch is a real IMAP feature that mail clients use to
// stream large attachments; a fix that returns nothing, or the whole body, for
// every spec would break them while making the test above pass.
func TestAValidPartialFetchStillReturnsThatSlice(t *testing.T) {
	body := []byte("0123456789")

	for _, tc := range []struct {
		spec string
		want string
	}{
		{"0.4", "0123"},
		{"2.3", "234"},
		{"6.99", "6789"}, // size past the end clamps to the end
		{"10.5", ""},     // offset exactly at the end is legal and empty
		{"3", "3456789"}, // bare offset means "to the end"
	} {
		got, origin, partial := applyPartialFetch(body, tc.spec)
		if string(got) != tc.want {
			t.Errorf("BODY[]<%s> = %q, want %q", tc.spec, got, tc.want)
		}
		if !partial {
			t.Errorf("BODY[]<%s> did not report itself as a partial fetch, so the response "+
				"would omit the <origin> octet the client is expecting", tc.spec)
		}
		_ = origin
	}
}

// An empty spec is not a partial at all and must return the whole body — this is
// the ordinary BODY[] path that every client uses for every message.
func TestAnAbsentPartialSpecReturnsTheWholeBody(t *testing.T) {
	body := []byte("the entire message")
	got, _, partial := applyPartialFetch(body, "")
	if string(got) != string(body) {
		t.Errorf("BODY[] returned %q, want the whole body", got)
	}
	if partial {
		t.Error("BODY[] with no <partial> reported itself as partial; the response would " +
			"carry an <origin> octet that the client did not ask for")
	}
}
