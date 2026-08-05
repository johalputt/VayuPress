// SPDX-License-Identifier: Apache-2.0

//go:build linux

package vayuveil

// hostposture_linux.go — reading the two host facts S5 reports.
//
// Everything here READS. Nothing is arranged, and nothing could be: Secure Boot
// is decided by firmware before this process exists, and a TPM is either
// soldered on or not.

import "os"

// readHostPosture gathers the host facts.
//
// The readers are injected for the same reason readSandbox's are: a test host
// cannot produce the states that matter. A machine with Secure Boot disabled, or
// with no efivars at all, is exactly the configuration whose row is worth
// getting right, and a suite that could only observe the machine it runs on
// would never see it.
func readHostPosture(readFile func(string) ([]byte, error),
	readDir func(string) ([]string, error)) HostPosture {
	h := HostPosture{Supported: true}

	if raw, err := readFile(secureBootVar); err == nil {
		h.SecureBoot, h.SecureBootKnown = parseSecureBoot(raw)
	}

	// A missing /sys/class/tpm is the answer "no TPM", not "unknown": the
	// directory exists on every kernel with TPM support compiled in, and its
	// absence on a kernel without it still means this host has no usable TPM.
	if entries, err := readDir("/sys/class/tpm"); err == nil {
		h.TPMPresent, h.TPMKnown = tpmPresent(entries), true
	} else if os.IsNotExist(err) {
		h.TPMPresent, h.TPMKnown = false, true
	}

	return h
}

// ReadHostPosture reads the live state.
func ReadHostPosture() HostPosture {
	return readHostPosture(
		func(p string) ([]byte, error) { return os.ReadFile(p) }, // #nosec G304 -- fixed sysfs path
		func(p string) ([]string, error) {
			ents, err := os.ReadDir(p)
			if err != nil {
				return nil, err
			}
			out := make([]string, 0, len(ents))
			for _, e := range ents {
				out = append(out, e.Name())
			}
			return out, nil
		})
}
