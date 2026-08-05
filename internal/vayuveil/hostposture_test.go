// SPDX-License-Identifier: Apache-2.0

package vayuveil

import (
	"strings"
	"testing"
)

// THE trap in this file, and the reason parseSecureBoot exists as a named
// function rather than an inline byte index.
//
// An EFI variable's payload is four bytes of attributes followed by the value.
// Reading byte ZERO returns an attribute bitmask that is non-zero on essentially
// every machine — so the naive implementation reports Secure Boot as ENABLED
// everywhere, including on the hosts where saying so is most wrong. A security
// page claiming the firmware checked a signature when nothing did is the §8 lie
// in its most convincing form, because it looks like good news.
func TestSecureBootIsReadFromTheValueByteNotTheAttributes(t *testing.T) {
	// Real shape: attributes 0x06 0x00 0x00 0x00, then the value.
	on, known := parseSecureBoot([]byte{0x06, 0x00, 0x00, 0x00, 0x01})
	if !known || !on {
		t.Errorf("an enabled variable read as on=%v known=%v", on, known)
	}
	off, known := parseSecureBoot([]byte{0x06, 0x00, 0x00, 0x00, 0x00})
	if !known {
		t.Fatal("a disabled variable read as unknown")
	}
	if off {
		t.Error("Secure Boot is DISABLED and was reported as enabled — the attribute bytes are " +
			"being read as the value, which reports every machine as protected")
	}
	// Short or empty payloads are unknown, never a value.
	for _, raw := range [][]byte{nil, {}, {0x06}, {0x06, 0x00, 0x00, 0x00}} {
		if _, known := parseSecureBoot(raw); known {
			t.Errorf("a %d-byte payload was read as a definite answer", len(raw))
		}
	}
}

func TestTPMPresenceIsDecidedByADeviceNotByTheDirectoryExisting(t *testing.T) {
	if !tpmPresent([]string{"tpm0"}) {
		t.Error("tpm0 is not recognised")
	}
	if !tpmPresent([]string{"tpmrm0", "tpm0"}) {
		t.Error("a device list containing a TPM is not recognised")
	}
	// An EMPTY /sys/class/tpm means the kernel has TPM support and no device.
	// Reading the directory's existence as presence would report a TPM on every
	// kernel built with the driver.
	if tpmPresent(nil) || tpmPresent([]string{}) {
		t.Error("an empty tpm class directory is reported as a TPM being present")
	}

	// AND a directory holding entries that are not TPM devices. A sysfs class
	// directory can carry `uevent`, `power`, `subsystem` and similar without any
	// device being present — this case was found by a mutation that replaced the
	// "tpm" prefix with an empty string, which matches everything and passed a
	// test that only ever offered it empty lists. An empty-list assertion does
	// not test a prefix; it tests a length.
	if tpmPresent([]string{"uevent", "power", "subsystem"}) {
		t.Error("a class directory with no TPM device is reported as a TPM being present; " +
			"the prefix is not being applied")
	}
	// A device whose name merely CONTAINS tpm elsewhere is not a TPM node.
	if tpmPresent([]string{"not-a-tpm-really"}) {
		t.Error("a device matched on a substring rather than a prefix")
	}
}

// Neither row may read as something VayuVeil provides, and neither may read as
// attestation. "Secure Boot: enabled" on a security page is the sentence most
// likely to be quoted back as a guarantee.
func TestTheHostRowsSayTheyAreNotOursAndNotAttestation(t *testing.T) {
	for name, h := range map[string]HostPosture{
		"enabled":  {Supported: true, SecureBoot: true, SecureBootKnown: true},
		"disabled": {Supported: true, SecureBoot: false, SecureBootKnown: true},
		"unknown":  {Supported: true},
	} {
		d := h.DescribeSecureBoot()
		if name != "unknown" || true {
			if !strings.Contains(d, "not something VayuVeil provides") &&
				!strings.Contains(d, "does not expose") {
				t.Errorf("%s: the row does not disclaim ownership: %q", name, d)
			}
		}
		if h.Supported && !strings.Contains(d, "NOT attestation") {
			t.Errorf("%s: the row does not say it is not attestation: %q", name, d)
		}
	}

	// The enabled row must also say what it does NOT prove, or it reads as a
	// running-system guarantee rather than a boot-time signature check.
	on := HostPosture{Supported: true, SecureBoot: true, SecureBootKnown: true}
	if !strings.Contains(on.DescribeSecureBoot(), "modified since") {
		t.Errorf("the enabled row does not say it says nothing about the running kernel: %q",
			on.DescribeSecureBoot())
	}

	// And a present TPM must not read as protection while nothing is sealed to it.
	tpm := HostPosture{Supported: true, TPMPresent: true, TPMKnown: true}
	if !strings.Contains(tpm.DescribeTPM(), "does not use") {
		t.Errorf("a present TPM reads as protection: %q", tpm.DescribeTPM())
	}
}
