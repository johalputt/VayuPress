// SPDX-License-Identifier: Apache-2.0

package vayuveil

import "testing"

// A REAL mountinfo from a systemd service with PrivateDevices=yes,
// PrivateTmp=yes and ProtectHome=yes, trimmed to the lines that matter.
const sandboxedMountinfo = `21 62 0:20 / /sys rw,nosuid,nodev,noexec,relatime shared:7 - sysfs sysfs rw
22 62 0:5 / /proc rw,nosuid,nodev,noexec,relatime shared:14 - proc proc rw
23 62 0:6 / /dev rw,nosuid shared:2 - tmpfs tmpfs rw,size=3283468k,nr_inodes=819200,mode=755
24 23 0:23 / /dev/pts rw,nosuid,noexec,relatime shared:3 - devpts devpts rw,gid=5,mode=620
25 23 0:24 / /dev/shm rw,nosuid,nodev shared:4 - tmpfs tmpfs rw
31 62 0:28 /private/tmp /tmp rw,nosuid,nodev,relatime shared:16 - tmpfs tmpfs rw
40 62 0:29 / /home ro,nosuid,nodev,noexec,relatime shared:20 - tmpfs tmpfs ro,mode=755`

// The same host with NO service sandbox: /dev/pts and /dev/shm are still there,
// because they are on every Linux system ever booted, but /dev itself is not a
// mount of its own.
const unsandboxedMountinfo = `21 62 0:20 / /sys rw,nosuid,nodev,noexec,relatime shared:7 - sysfs sysfs rw
22 62 0:5 / /proc rw,nosuid,nodev,noexec,relatime shared:14 - proc proc rw
24 62 0:23 / /dev/pts rw,nosuid,noexec,relatime shared:3 - devpts devpts rw,gid=5,mode=620
25 62 0:24 / /dev/shm rw,nosuid,nodev shared:4 - tmpfs tmpfs rw
30 62 8:1 / / rw,relatime shared:1 - ext4 /dev/sda1 rw`

// THE test for this file. A prefix match on "/dev" is satisfied by /dev/pts,
// which exists on every Linux host including one with no sandbox whatsoever —
// so a prefix-matching implementation reports a private /dev on every machine,
// forever, and the operator is told a control is holding that was never applied.
//
// That is the worst failure this subsystem can have: not a missing feature, but
// a green row that means nothing. It is also this repository's own standing
// note — an assertion that cannot say which thing it matched is not an
// assertion.
func TestAPrivateDevIsNotInferredFromDevPtsExisting(t *testing.T) {
	if !mountedOn(sandboxedMountinfo, "/dev") {
		t.Error("a genuinely private /dev was not detected")
	}
	if mountedOn(unsandboxedMountinfo, "/dev") {
		t.Error("an unsandboxed host reports a private /dev — /dev/pts and /dev/shm are mounted on " +
			"EVERY Linux system, so this control would read as holding on every machine on earth")
	}
	// And the other two, which have the same trap in a milder form.
	if !mountedOn(sandboxedMountinfo, "/tmp") || !mountedOn(sandboxedMountinfo, "/home") {
		t.Error("PrivateTmp / ProtectHome not detected on a sandboxed mount table")
	}
	if mountedOn(unsandboxedMountinfo, "/tmp") || mountedOn(unsandboxedMountinfo, "/home") {
		t.Error("PrivateTmp / ProtectHome reported on an unsandboxed host")
	}
}

// A mount point containing a space is escaped as \040 in mountinfo. Not exotic:
// it is what the kernel does, and an unescaped comparison silently stops
// matching rather than failing loudly.
func TestAMountPointWithASpaceIsStillMatched(t *testing.T) {
	const mi = `31 62 0:28 / /mnt/my\040disk rw,relatime shared:16 - tmpfs tmpfs rw`
	if !mountedOn(mi, "/mnt/my disk") {
		t.Error("an escaped mount point was not unescaped before comparison")
	}
}

// Unknown must never round up to protected. This is the rounding error ADR-0150
// §4 calls the most dangerous one the report can make.
func TestAnUnreadableMountTableIsNotReportedAsADeniedDevice(t *testing.T) {
	for name, s := range map[string]SandboxState{
		"never read":            {Supported: true},
		"read, and not private": {Supported: true, PrivateDevKnown: true, PrivateDev: false},
	} {
		if s.DeniedDeviceCapture() {
			t.Errorf("%s: reported as denying capture devices", name)
		}
	}
	ok := SandboxState{Supported: true, PrivateDevKnown: true, PrivateDev: true}
	if !ok.DeniedDeviceCapture() {
		t.Error("a verified private /dev is not credited, so real protection goes unreported")
	}
}

// An empty capability set and an unreadable one are different facts, and the
// difference is the whole discipline of this package: zero is excellent news,
// unknown is no news, and a type that cannot tell them apart will eventually
// report one as the other.
func TestNoCapabilitiesAndUnknownCapabilitiesReadDifferently(t *testing.T) {
	unknown := SandboxState{Supported: true}.DescribeCaps()
	none := SandboxState{Supported: true, CapEffKnown: true, CapEff: 0}.DescribeCaps()
	if unknown == none {
		t.Fatal("an unreadable capability set reads identically to an empty one")
	}
	if !contains(unknown, "unverified") {
		t.Errorf("the unknown case does not say it is unverified: %q", unknown)
	}
	if !contains(none, "NO capabilities") {
		t.Errorf("the empty case does not say so plainly: %q", none)
	}

	// The shipped unit's exact grant is named rather than printed as hex.
	shipped := SandboxState{Supported: true, CapEffKnown: true, CapEff: 1 << CapNetBindService}.DescribeCaps()
	if !contains(shipped, "CAP_NET_BIND_SERVICE") {
		t.Errorf("the shipped grant is not named: %q", shipped)
	}
	// Anything beyond it is a finding, not a shrug.
	extra := SandboxState{Supported: true, CapEffKnown: true, CapEff: 1<<CapNetBindService | 1<<21}.DescribeCaps()
	if !contains(extra, "beyond") {
		t.Errorf("holding more than the unit grants is not flagged: %q", extra)
	}
}

// The device row is the one an operator acts on, so its three states must read
// as three different things.
func TestTheDeviceRowDistinguishesUnknownFromDeniedFromShared(t *testing.T) {
	unknown := SandboxState{Supported: true}.DescribeDevices()
	denied := SandboxState{Supported: true, PrivateDevKnown: true, PrivateDev: true}.DescribeDevices()
	shared := SandboxState{Supported: true, PrivateDevKnown: true, PrivateDev: false}.DescribeDevices()
	if unknown == denied || denied == shared || unknown == shared {
		t.Fatal("two of the three device states render identically")
	}
	if !contains(denied, "PRIVATE /dev") || !contains(denied, "not merely missing") {
		t.Errorf("the denied case does not distinguish denial from absence: %q", denied)
	}
	if !contains(shared, "PrivateDevices=yes") {
		t.Errorf("the shared case does not tell the operator which directive is missing: %q", shared)
	}
	// Scope must be attached wherever a control is claimed, or the reader takes
	// it for the machine.
	if !contains(denied, "the rest of the machine is unaffected") {
		t.Errorf("the denied row does not state its scope: %q", denied)
	}
}

func TestStatusFieldsAreParsedFromRealProcText(t *testing.T) {
	const status = "Name:\tvayupress\nUmask:\t0022\nState:\tS (sleeping)\n" +
		"Seccomp:\t2\nSeccomp_filters:\t1\nNoNewPrivs:\t1\n" +
		"CapInh:\t0000000000000000\nCapPrm:\t0000000000000400\n" +
		"CapEff:\t0000000000000400\nCapBnd:\t0000000000000400\n"

	if v, ok := parseStatusField(status, "NoNewPrivs"); !ok || v != "1" {
		t.Errorf("NoNewPrivs parsed as %q ok=%v", v, ok)
	}
	// Seccomp_filters starts with "Seccomp" — a prefix-matching parser returns
	// the wrong line's value, and the report would show a filter count as a mode.
	if v, ok := parseStatusField(status, "Seccomp"); !ok || v != "2" {
		t.Errorf("Seccomp parsed as %q (want 2); a prefix match caught Seccomp_filters", v)
	}
	if m, ok := parseCapMask(status, "CapEff"); !ok || m != 1<<CapNetBindService {
		t.Errorf("CapEff parsed as %#x ok=%v, want CAP_NET_BIND_SERVICE", m, ok)
	}
	if _, ok := parseCapMask(status, "CapNope"); ok {
		t.Error("a missing mask was reported as read")
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}
