// SPDX-License-Identifier: Apache-2.0

package vayuveil

// hostposture.go — ADR-0150 §5 S5. Reading what the host already has.
//
// The distinction this file exists to hold, and it is the whole reason S5 is
// worded the way it is: **reporting a property of the host is not providing
// it.** Secure Boot is a fact about how this machine started. VayuVeil did not
// arrange it, cannot arrange it, and gains nothing it may claim from it. What it
// can honestly do is tell an operator what is true, because an operator deciding
// how much to trust this install benefits from knowing whether anything checked
// the boot chain.
//
// It is emphatically NOT attestation. Attestation means measuring the boot
// sequence and proving the result to a remote party. Nothing here measures
// anything and nothing is proven to anyone: this reads two files the kernel
// exposes and repeats what they say. ADR-0150 §5 lists measured boot and remote
// attestation as permanently out of reach, and a row on this page that let a
// reader think otherwise would be the §8 lie in its most tempting form — because
// "Secure Boot: enabled" looks like a guarantee and is not one.

import "strings"

// HostPosture is what the host itself offers, read rather than arranged.
type HostPosture struct {
	// Supported is whether this platform exposes any of it.
	Supported bool

	// SecureBoot is the firmware's answer. Known is separate because "the
	// variable could not be read" and "the variable says off" are different
	// facts, and only one of them is a finding.
	SecureBoot, SecureBootKnown bool

	// TPMPresent is whether a TPM device exists. It says nothing about whether
	// anything is sealed to it — this install seals nothing to a TPM, and a row
	// implying otherwise would be crediting hardware for work nobody did.
	TPMPresent, TPMKnown bool
}

// secureBootVar is the EFI variable, with the vendor GUID that makes it the
// global one rather than a vendor's private copy.
const secureBootVar = "/sys/firmware/efi/efivars/SecureBoot-8be4df61-93ca-11d2-aa0d-00e098032b8c"

// parseSecureBoot reads the EFI variable's payload.
//
// The layout is four bytes of attributes followed by the value, so the answer is
// the FIFTH byte and not the first. Reading byte zero returns an attribute
// bitmask that is non-zero on essentially every machine, which would report
// Secure Boot as enabled everywhere — including on the hosts where saying so is
// most wrong.
func parseSecureBoot(raw []byte) (on bool, known bool) {
	if len(raw) < 5 {
		return false, false
	}
	return raw[4] == 1, true
}

// DescribeSecureBoot renders the row.
func (h HostPosture) DescribeSecureBoot() string {
	const notOurs = " This is a property of the host, not something VayuVeil provides or could " +
		"provide, and it is NOT attestation: nothing here measures the boot sequence or proves " +
		"anything to a remote party."
	switch {
	case !h.Supported:
		return "This platform does not expose a Secure Boot state that this process can read."
	case !h.SecureBootKnown:
		return "The Secure Boot EFI variable could not be read — this may be a system that boots " +
			"without EFI, or one where efivars is not mounted. Reported as unverified rather " +
			"than as disabled." + notOurs
	case h.SecureBoot:
		return "Secure Boot is enabled, so the firmware checked a signature on what it booted. " +
			"That says nothing about whether the running kernel has been modified since." + notOurs
	default:
		return "Secure Boot is disabled: nothing checked a signature on the bootloader or kernel " +
			"this host started. An attacker who can write to the boot partition can replace " +
			"either, and no control in this binary would notice." + notOurs
	}
}

// DescribeTPM renders the TPM row.
func (h HostPosture) DescribeTPM() string {
	switch {
	case !h.Supported, !h.TPMKnown:
		return "Whether this host has a TPM could not be determined, so nothing is claimed either way."
	case h.TPMPresent:
		return "A TPM is present on this host. Nothing in this install seals anything to it — the " +
			"keystore key is host-bound by file, not by hardware — so this is reported as a " +
			"capability the machine has and this software does not use."
	default:
		return "No TPM is present. That is not a fault: nothing here requires one, and the keystore " +
			"key is host-bound by file rather than sealed to hardware."
	}
}

// tpmPresent reports whether any TPM device is exposed.
func tpmPresent(entries []string) bool {
	for _, e := range entries {
		if strings.HasPrefix(e, "tpm") {
			return true
		}
	}
	return false
}
