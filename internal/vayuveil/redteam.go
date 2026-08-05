// SPDX-License-Identifier: Apache-2.0

package vayuveil

// redteam.go — ADR-0150 §6's capture suite.
//
// "The suite is the specification. A technique that is not in it is not
// defended, and the report must not imply otherwise."
//
// Two rules shape every line below.
//
// ARTIFACT-LEVEL, NOT TRANSPORT-LEVEL. Each technique tries to end up HOLDING
// CONTENT and is judged on whether it did. Not on whether open() returned an
// error, not on whether a call was refused — on bytes. A subsystem that returns
// the right status while producing the wrong bytes passes every transport check
// ever written, which is the lesson this project already paid for once.
//
// NOT-ATTEMPTED IS NOT A PASS. Techniques this binary cannot run — a Wayland
// screencopy handshake needs a Wayland client, an AT-SPI dump needs a session
// bus — are reported as NOT ATTEMPTED, with the reason, and are never counted as
// a defence. A suite that silently skips what it cannot do reports a clean sweep
// it did not perform, and that is a worse outcome than having no suite.

import (
	"os"
	"strconv"
)

// AttackOutcome is what one technique achieved.
type AttackOutcome uint8

const (
	// outcomeUnset is not a valid answer.
	outcomeUnset AttackOutcome = iota
	// AttackNotAttempted — this binary cannot run the technique. NEVER a pass.
	AttackNotAttempted
	// AttackNothingPresent — the interface the technique needs is not on this
	// host, so there was nothing to capture. Not a defence either: it is the
	// absence of the target, not the presence of a control.
	AttackNothingPresent
	// AttackRefused — the technique ran and came away with NO content.
	AttackRefused
	// AttackCapturedContent — the technique ran and came away HOLDING CONTENT.
	// This is the finding the suite exists to produce.
	AttackCapturedContent
)

// AttackResult is one technique's run.
type AttackResult struct {
	Technique string
	Asset     Asset
	Outcome   AttackOutcome
	// Bytes is how much content the technique actually obtained. The number that
	// decides the outcome, rather than an error value.
	Bytes int
	// Detail says what was tried and what came back.
	Detail string
	// ViaDeviceNode marks a technique that reaches its asset through a device
	// node under /dev.
	//
	// It exists so the report can tell two very different causes of "nothing
	// present" apart. On a service with PrivateDevices=yes the device nodes are
	// not missing from the machine — they are REFUSED to this process by a
	// control that is verifiably in force. Without this flag both cases render
	// as "the absence of a device, not the presence of a control", which
	// understates real protection on every correctly deployed install.
	//
	// A bool on the result rather than a string match on the technique name: a
	// report that decides policy by matching prose breaks the first time
	// somebody rewords the prose.
	ViaDeviceNode bool
}

// RunRedTeam runs every technique this binary can run against the given host and
// reports what each came away holding.
//
// reader is injected so the suite is testable: a fake that returns bytes proves
// the assertions bite, which a suite that only ever runs on a machine where
// nothing is present cannot do.
func RunRedTeam(h Host, read func(path string, max int) ([]byte, error)) []AttackResult {
	out := []AttackResult{
		captureFromDeviceFamily(h, read, "/dev/fb*", "read the framebuffer directly", AssetFramebuffer),
		captureFromDeviceFamily(h, read, "/dev/input/event*", "read keystrokes from evdev", AssetKeyboard),
		captureFromDeviceFamily(h, read, "/dev/dri/card*", "read back another client's buffer via a DRM card node", AssetWindowPixels),
		captureOtherProcessMemory(h, read),
	}

	// Everything below needs a client library or a live session this binary does
	// not link. Named anyway, because §6 says a technique that is not in the
	// suite is not defended — and a reader has to be able to see the gap.
	for _, na := range []struct {
		technique string
		asset     Asset
		why       string
	}{
		{"wlr-screencopy a frame of the whole screen", AssetFramebuffer,
			"needs a Wayland client connection and the protocol bound; this binary links no Wayland client"},
		{"zwlr_export_dmabuf a GPU buffer handle", AssetFramebuffer,
			"needs a Wayland client and a DMA-BUF import path"},
		{"dump every window's text over AT-SPI", AssetWindowText,
			"needs a session-bus connection and the AT-SPI client library"},
		{"poll the clipboard through zwlr_data_control", AssetClipboard,
			"needs a Wayland client connection"},
		{"read the X11 root window", AssetFramebuffer,
			"needs an X11 client library; presence of a display is probed but no capture is attempted"},
		{"harvest a thumbnailer's cache of other people's documents", AssetThumbnails,
			"needs a per-user cache path this binary does not enumerate"},
		{"PTRACE_ATTACH to another process and read its registers", AssetMemoryImage,
			"attaching to a live process from inside a running server is unsafe to do on an operator's " +
				"machine; the ptrace_scope tunable is probed instead, which is weaker and is labelled so"},
	} {
		out = append(out, AttackResult{
			Technique: na.technique, Asset: na.asset,
			Outcome: AttackNotAttempted,
			Detail:  "NOT ATTEMPTED — " + na.why + ". This technique is therefore not defended and not tested.",
		})
	}
	return out
}

// captureFromDeviceFamily tries to actually read content out of a device family.
func captureFromDeviceFamily(h Host, read func(string, int) ([]byte, error),
	pattern, technique string, asset Asset) AttackResult {
	matches := h.Glob(pattern)
	if len(matches) == 0 {
		return AttackResult{Technique: technique, Asset: asset, ViaDeviceNode: true, Outcome: AttackNothingPresent,
			Detail: "nothing matches " + pattern + " on this host, so there was no target. That is " +
				"the absence of a device, not the presence of a control."}
	}
	var got int
	var from string
	for _, m := range matches {
		b, err := read(m, 4096)
		if err == nil && len(b) > 0 {
			got, from = len(b), m
			break
		}
	}
	if got > 0 {
		return AttackResult{Technique: technique, Asset: asset, ViaDeviceNode: true, Outcome: AttackCapturedContent,
			Bytes: got,
			Detail: "CAPTURED " + strconv.Itoa(got) + " bytes from " + from + ". This is content in " +
				"the attacker's hands, not an error code."}
	}
	return AttackResult{Technique: technique, Asset: asset, ViaDeviceNode: true, Outcome: AttackRefused,
		Detail: strconv.Itoa(len(matches)) + " node(s) match " + pattern + " and none yielded any " +
			"bytes to this process."}
}

// captureOtherProcessMemory tries to read another process's memory.
//
// It targets PID 1, which always exists and is owned by root: a same-uid read of
// it must fail. Reading our OWN /proc/self/mem always succeeds and would prove
// nothing at all, which is the trap in writing this test.
func captureOtherProcessMemory(h Host, read func(string, int) ([]byte, error)) AttackResult {
	const technique = "read another process's memory through /proc/<pid>/mem"
	const target = "/proc/1/mem"
	if !h.Exists(target) {
		return AttackResult{Technique: technique, Asset: AssetMemoryImage,
			Outcome: AttackNothingPresent,
			Detail: target + " does not exist on this host, so there was no target. That is the " +
				"absence of a procfs, not the presence of a control."}
	}
	b, err := read(target, 4096)
	if err == nil && len(b) > 0 {
		return AttackResult{Technique: technique, Asset: AssetMemoryImage,
			Outcome: AttackCapturedContent, Bytes: len(b),
			Detail: "CAPTURED " + strconv.Itoa(len(b)) + " bytes of another process's memory from " +
				target + "."}
	}
	return AttackResult{Technique: technique, Asset: AssetMemoryImage, Outcome: AttackRefused,
		Detail: target + " yielded no bytes to this process."}
}

// LiveRead is the real reader: it opens a path and takes at most max bytes.
//
// A short read or an error both come back as "no content", because the only
// question this suite asks is whether the attacker ended up holding something.
func LiveRead(path string, max int) ([]byte, error) {
	// O_NONBLOCK, deliberately. A read from an evdev node with nothing queued
	// blocks until a key is pressed, so opening the console on a machine with a
	// keyboard would hang the request until the operator happened to type. With
	// the flag the read returns EAGAIN at once, which this suite scores exactly
	// as it should: the attacker came away holding nothing.
	f, err := os.OpenFile(path, os.O_RDONLY|nonblockFlag, 0) // #nosec G304 -- fixed probe paths from the registry
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, max)
	n, err := f.Read(buf)
	if n <= 0 {
		if err == nil {
			err = errNoContent
		}
		return nil, err
	}
	return buf[:n], nil
}

var errNoContent = noContentError{}

type noContentError struct{}

func (noContentError) Error() string { return "no content read" }

// RedTeamSummary counts a run. `captured` is the only number that is a failure;
// `notAttempted` is reported separately and deliberately never folded into a
// pass, because the difference between "we tried and it failed" and "we did not
// try" is the whole value of the suite.
func RedTeamSummary(rs []AttackResult) (captured, refused, notPresent, notAttempted int) {
	for _, r := range rs {
		switch r.Outcome {
		case AttackCapturedContent:
			captured++
		case AttackRefused:
			refused++
		case AttackNothingPresent:
			notPresent++
		case AttackNotAttempted:
			notAttempted++
		}
	}
	return
}

// TechniquesNotAttempted names the gaps, for the page.
func TechniquesNotAttempted(rs []AttackResult) []string {
	var out []string
	for _, r := range rs {
		if r.Outcome == AttackNotAttempted {
			out = append(out, r.Technique)
		}
	}
	return out
}
