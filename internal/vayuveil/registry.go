// SPDX-License-Identifier: Apache-2.0

package vayuveil

// registry.go — every interface through which observation is possible.
//
// Adding a channel here means answering four questions. Adding one WITHOUT
// answering them is a failing test, not a review miss. Removing one because it
// is inconvenient is visible in a diff.
//
// The Rationale field is not documentation. It is the answer to "why is this the
// right disposition", written down at the moment somebody knew, and it is what a
// reader checks the policy against later.

// channels is the registry. Every ChannelID appears exactly once.
var channels = []Channel{
	// ── Asset 1–2: the framebuffer and individual surfaces ────────────────────
	{
		ID: "wlr-screencopy", Name: "wlr-screencopy protocol",
		Asset: AssetFramebuffer, Default: DispositionAbsent,
		Grant: GrantNone, Indicator: IndicatorNone, Audit: AuditNone,
		Phase: PhaseP1Screen,
		Rationale: "A Wayland client cannot read the screen unless the compositor hands it over, and " +
			"this is the protocol that hands it over with no prompt at all. P1 does not gate it — it " +
			"is not compiled into the compositor. An absent protocol has no bypass, which is the only " +
			"reason this row can honestly read Absent rather than Deny. Audit is None because there " +
			"is no request to record: nothing can speak a protocol the compositor does not implement.",
		Probe: func(h Host) Observation {
			return probeSocket(h, "WAYLAND_DISPLAY", "", "Wayland session")
		},
	},
	{
		ID: "wlr-export-dmabuf", Name: "zwlr_export_dmabuf protocol",
		Asset: AssetFramebuffer, Default: DispositionAbsent,
		Grant: GrantNone, Indicator: IndicatorNone, Audit: AuditNone,
		Phase: PhaseP1Screen,
		Rationale: "The zero-copy sibling of screencopy: it hands a client a GPU buffer handle for the " +
			"composited output. Same disposition and the same reason — absent from the compositor " +
			"rather than gated in it. Listed separately because closing screencopy and leaving this " +
			"open would be a complete bypass wearing a different protocol name.",
		Probe: func(h Host) Observation {
			return probeSocket(h, "WAYLAND_DISPLAY", "", "Wayland session")
		},
	},
	{
		ID: "xdg-portal-screencast", Name: "xdg-desktop-portal ScreenCast",
		Asset: AssetFramebuffer, Default: DispositionGrant,
		Grant: GrantScopedTimed, Indicator: IndicatorCompositor, Audit: AuditAll,
		Phase: PhaseP1Screen,
		Rationale: "The one path capture is allowed to take, because it is the one that asks. The " +
			"grant is scoped to a window or an output rather than to everything, time-boxed with a " +
			"visible countdown, and revocable mid-stream from a control the client cannot draw over. " +
			"Conferencing and remote support are legitimate; they are not a reason to stop asking.",
		Probe: func(h Host) Observation {
			return probeSocket(h, "", "/usr/libexec/xdg-desktop-portal", "desktop portal")
		},
	},
	{
		ID: "dev-fb", Name: "/dev/fb* framebuffer devices",
		Asset: AssetFramebuffer, Default: DispositionDeny,
		Grant: GrantNone, Indicator: IndicatorNone, Audit: AuditAll,
		Phase: PhaseP4Sandbox,
		Rationale: "A raw framebuffer node is the whole screen with no compositor in the way, so no " +
			"grant model can make it safe and none is offered. Denied by sandbox and MAC policy " +
			"rather than by absence, because the node legitimately exists for the console. Audited: " +
			"an open attempt against it is a finding on any desktop.",
		Probe: func(h Host) Observation { return probeGlob(h, "/dev/fb*", "framebuffer device") },
	},
	{
		ID: "dev-dri", Name: "/dev/dri render and card nodes",
		Asset: AssetWindowPixels, Default: DispositionDeny,
		Grant: GrantNone, Indicator: IndicatorNone, Audit: AuditAll,
		Phase: PhaseP4Sandbox,
		Rationale: "A card node can scan out and read back other clients' buffers. Each sandbox gets " +
			"its own render node and nothing else; the card nodes are denied outright. This is also " +
			"where GPU buffer zeroing on release belongs, since uninitialised VRAM has historically " +
			"handed one process the previous one's framebuffer.",
		Probe: func(h Host) Observation { return probeGlob(h, "/dev/dri/card*", "DRM card node") },
	},
	{
		ID: "x11-root-read", Name: "X11 root window read",
		Asset: AssetFramebuffer, Default: DispositionDeny,
		Grant: GrantNone, Indicator: IndicatorNone, Audit: AuditAll,
		Phase: PhaseP4Sandbox,
		Rationale: "On X11 any process running as you reads the entire screen — every window, every " +
			"password field — with no permission, no indicator and no log. That is not a bug to fix; " +
			"X11 predates the threat and will never have an equivalent chokepoint. The disposition is " +
			"therefore about not running X11 as a shared session at all, and the report says so " +
			"rather than implying a policy can hold here.",
		Probe: func(h Host) Observation {
			return probeSocket(h, "DISPLAY", "/tmp/.X11-unix/X0", "X11 display")
		},
	},
	{
		ID: "xwayland-shared", Name: "shared XWayland instance",
		Asset: AssetWindowPixels, Default: DispositionDeny,
		Grant: GrantNone, Indicator: IndicatorNone, Audit: AuditAll,
		Phase: PhaseP4Sandbox,
		Rationale: "This matters more than it looks. A single XWayland shared between sandboxes " +
			"reintroduces X11's flaw wholesale, because the X11 clients inside it can see each other " +
			"however well the Wayland side is isolated. One rootless instance per sandbox restores " +
			"the isolation; a shared one silently undoes P4.",
		Probe: func(h Host) Observation {
			return probeSocket(h, "", "/tmp/.X11-unix/X1", "shared XWayland socket")
		},
	},

	// ── Asset 3: window content as text ───────────────────────────────────────
	{
		ID: "at-spi", Name: "AT-SPI accessibility bus",
		Asset: AssetWindowText, Default: DispositionGrant,
		Grant: GrantPinned, Indicator: IndicatorPanel, Audit: AuditAll,
		Phase: PhaseP3Accessibility,
		Rationale: "A complete capture channel that reads no pixels, and it is almost always left " +
			"wide open. AT-SPI can dump the full text of every window, which defeats a screenshot " +
			"policy entirely while looking like nothing. It is grantable rather than denied because a " +
			"screen reader is a legitimate and important use — a privacy feature that breaks " +
			"assistive technology is a failure with a moral dimension, not a trade-off. The grant is " +
			"Pinned because a screen-reader user cannot be asked on every window, and pinned grants " +
			"are listed permanently in the panel for exactly that reason. The indicator is Panel and " +
			"not Compositor, which is weaker, and the page says so.",
		Probe: func(h Host) Observation {
			return probeSocket(h, "AT_SPI_BUS_ADDRESS", "", "accessibility bus")
		},
	},

	// ── Assets 4–5: keyboard and pointer ──────────────────────────────────────
	{
		ID: "dev-input", Name: "/dev/input event devices",
		Asset: AssetKeyboard, Default: DispositionDeny,
		Grant: GrantNone, Indicator: IndicatorNone, Audit: AuditAll,
		Phase: PhaseP4Sandbox,
		Rationale: "Raw evdev is a keylogger with no compositor between it and every keystroke, " +
			"including the ones typed into another application's password field. There is no grant " +
			"model for this because there is no scoped version of it: a process reading evdev reads " +
			"everything typed anywhere.",
		Probe: func(h Host) Observation { return probeGlob(h, "/dev/input/event*", "input event device") },
	},
	{
		ID: "virtual-keyboard", Name: "zwp_virtual_keyboard protocol",
		Asset: AssetKeyboard, Default: DispositionGrant,
		Grant: GrantPerUse, Indicator: IndicatorCompositor, Audit: AuditAll,
		Phase: PhaseP2Input,
		Rationale: "Injection rather than capture, and it belongs here because a process that can " +
			"type can drive another application into revealing what capture would have shown. " +
			"Granted per use, never remembered, and the prompt is worded bluntly: a keyboard grant is " +
			"worse than a screenshot grant and must not read like the same request.",
		Probe: func(h Host) Observation {
			return probeSocket(h, "WAYLAND_DISPLAY", "", "Wayland session")
		},
	},
	{
		ID: "input-method", Name: "input-method protocol",
		Asset: AssetKeyboard, Default: DispositionGrant,
		Grant: GrantPinned, Indicator: IndicatorPanel, Audit: AuditAll,
		Phase: PhaseP2Input,
		Rationale: "An input method sees every keystroke by construction — that is its job, and it is " +
			"indispensable for anyone typing a language that needs one. Pinned rather than per-use " +
			"for the same reason as the screen reader, and listed permanently so the person knows " +
			"which process holds it.",
		Probe: func(h Host) Observation {
			return probeSocket(h, "WAYLAND_DISPLAY", "", "Wayland session")
		},
	},
	{
		ID: "pointer-position", Name: "global pointer position",
		Asset: AssetPointer, Default: DispositionDeny,
		Grant: GrantNone, Indicator: IndicatorNone, Audit: AuditGrantsOnly,
		Phase: PhaseP2Input,
		Rationale: "A client learns the pointer only while it is over that client's own surface. " +
			"Global position is denied with no grant path: it reveals what a person is reading and " +
			"where they are about to click across every other window, and no application needs it " +
			"badly enough to be worth a prompt. Audit is GrantsOnly because there is no grant to " +
			"take and therefore no refusal stream worth keeping — the deny is structural.",
		Probe: func(h Host) Observation {
			return probeSocket(h, "WAYLAND_DISPLAY", "", "Wayland session")
		},
	},

	// ── Asset 6: clipboard ────────────────────────────────────────────────────
	{
		ID: "data-control", Name: "zwlr_data_control clipboard read",
		Asset: AssetClipboard, Default: DispositionGrant,
		Grant: GrantPerUse, Indicator: IndicatorCompositor, Audit: AuditAll,
		Phase: PhaseP2Input,
		Rationale: "Ambient clipboard reading is how a password manager's output leaves the machine. " +
			"Reads are per-paste rather than continuous: a clipboard manager that watches every copy " +
			"is a keylogger for the subset of text people are most careful about.",
		Probe: func(h Host) Observation {
			return probeSocket(h, "WAYLAND_DISPLAY", "", "Wayland session")
		},
	},

	// ── Assets 7–9: notifications, metadata, thumbnails ───────────────────────
	{
		ID: "notification-bodies", Name: "notification body text",
		Asset: AssetNotifications, Default: DispositionGrant,
		Grant: GrantPinned, Indicator: IndicatorPanel, Audit: AuditAll,
		Phase: PhaseP3Accessibility,
		Rationale: "Notification bodies carry one-time codes and message previews, so a process " +
			"subscribed to them reads the contents of a second factor. Pinned because a notification " +
			"daemon needs standing access, and listed permanently because that is a large amount of " +
			"trust to hold invisibly.",
		Probe: func(h Host) Observation {
			return probeSocket(h, "DBUS_SESSION_BUS_ADDRESS", "", "session bus")
		},
	},
	{
		ID: "window-metadata", Name: "window titles and application list",
		Asset: AssetWindowMetadata, Default: DispositionDeny,
		Grant: GrantNone, Indicator: IndicatorNone, Audit: AuditAll,
		Phase: PhaseP1Screen,
		Rationale: "Metadata leaks intent without leaking a single pixel: a list of window titles " +
			"says which bank, which patient, which lawyer. Denied rather than granted because the " +
			"legitimate consumers — a taskbar, a switcher — are compositor components rather than " +
			"clients, so nothing outside the trusted base needs to ask.",
		Probe: func(h Host) Observation {
			return probeSocket(h, "WAYLAND_DISPLAY", "", "Wayland session")
		},
	},
	{
		ID: "thumbnailer", Name: "file thumbnailer previews",
		Asset: AssetThumbnails, Default: DispositionDeny,
		Grant: GrantNone, Indicator: IndicatorNone, Audit: AuditAll,
		Phase: PhaseP4Sandbox,
		Rationale: "A thumbnailer renders other people's documents and writes the result to a cache " +
			"directory that is usually world-readable within the account. It is capture by proxy: no " +
			"screen is read, and the contents end up on disk anyway. Denied outside the sandbox that " +
			"owns the file.",
		Probe: func(h Host) Observation {
			return Observation{PresenceUnknown,
				"thumbnail cache contents are not inspected from here; presence says nothing about what a " +
					"thumbnailer has already rendered"}
		},
	},

	// ── Asset 10: memory images ───────────────────────────────────────────────
	{
		ID: "proc-pid-mem", Name: "/proc/<pid>/mem and ptrace",
		Asset: AssetMemoryImage, Default: DispositionDeny,
		Grant: GrantNone, Indicator: IndicatorNone, Audit: AuditAll,
		Phase: PhaseP4Sandbox,
		Rationale: "Reading another process's memory reaches the decoded framebuffer, the decrypted " +
			"message and the typed passphrase in one step, so it subsumes most of the channels above. " +
			"Denied by namespace and by yama ptrace_scope. The probe reports the kernel's scope value " +
			"because a machine with ptrace_scope=0 has this wide open regardless of what policy says.",
		Probe: func(h Host) Observation {
			v := trimmed(h.ReadFile("/proc/sys/kernel/yama/ptrace_scope"))
			switch v {
			case "":
				return Observation{PresenceUnknown, "yama ptrace_scope not readable; kernel may lack the LSM"}
			case "0":
				return Observation{PresentReachable,
					"yama ptrace_scope=0 — any process running as this user can attach to any other"}
			default:
				return Observation{PresentUnreachable, "yama ptrace_scope=" + v + " — attach is restricted"}
			}
		},
	},
	{
		ID: "core-dumps", Name: "core dumps of capture-capable processes",
		Asset: AssetMemoryImage, Default: DispositionDeny,
		Grant: GrantNone, Indicator: IndicatorNone, Audit: AuditGrantsOnly,
		Phase: PhaseP5Hygiene,
		Rationale: "A core dump of a compositor or a browser is a framebuffer and a session on disk, " +
			"written by the kernel to a path the person never chose. Disabled for capture-capable " +
			"processes. Audit is GrantsOnly because the kernel does not report refusals to us — " +
			"claiming AuditAll here would be a promise this install cannot keep.",
		Probe: func(h Host) Observation {
			v := trimmed(h.ReadFile("/proc/sys/kernel/core_pattern"))
			if v == "" {
				return Observation{PresenceUnknown, "core_pattern not readable"}
			}
			if v == "|/bin/false" || v == "/dev/null" {
				return Observation{PresenceAbsent, "core_pattern=" + v + " — dumps discarded"}
			}
			return Observation{PresentReachable, "core_pattern=" + v + " — dumps are written somewhere"}
		},
	},
	{
		ID: "hibernate-image", Name: "suspend and hibernate images",
		Asset: AssetMemoryImage, Default: DispositionDeny,
		Grant: GrantNone, Indicator: IndicatorNone, Audit: AuditGrantsOnly,
		Phase: PhaseP5Hygiene,
		Rationale: "A hibernate image contains the framebuffer, so an encrypted disk with an " +
			"unencrypted swap defeats itself the first time the lid closes. Either swap is encrypted " +
			"or hibernation is off; there is no third answer. Audit is GrantsOnly for the same reason " +
			"as core dumps — the kernel writes the image without asking anything here.",
		Probe: func(h Host) Observation {
			v := trimmed(h.ReadFile("/sys/power/state"))
			if v == "" {
				return Observation{PresenceUnknown, "/sys/power/state not readable"}
			}
			if containsWord(v, "disk") {
				return Observation{PresentReachable, "/sys/power/state offers 'disk' — hibernation is available"}
			}
			return Observation{PresenceAbsent, "/sys/power/state = " + v + " — no hibernate target"}
		},
	},

	// ── VayuOS's own capture path ─────────────────────────────────────────────
	{
		ID: "vayuos-own-capture", Name: "VayuOS's own screenshot path",
		Asset: AssetFramebuffer, Default: DispositionGrant,
		Grant: GrantPerUse, Indicator: IndicatorCompositor, Audit: AuditAll,
		Phase: PhaseP1Screen,
		Rationale: "Registered because the product is the thing best placed to violate this. VayuOS " +
			"gets no exemption from its own policy: its capture path is a channel like any other, " +
			"asks like any other, and appears in the same audit log. ADR-0150 §3.3 also forbids a " +
			"Recall-equivalent outright — no periodic snapshotting, no screen index, no timeline, not " +
			"even opt-in — because the moment VayuOS keeps a searchable history of a screen it IS the " +
			"threat, whatever the encryption.",
		Probe: func(h Host) Observation {
			return Observation{PresenceAbsent,
				"no screenshot path is compiled into this binary, and no screen index exists to inspect"}
		},
	},
}

func trimmed(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	return s
}

func containsWord(haystack, word string) bool {
	for _, f := range splitFields(haystack) {
		if f == word {
			return true
		}
	}
	return false
}

func splitFields(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

// Channels returns the registry. A copy, so a caller cannot edit the contract by
// accident — the same reason vayushield.EnforcementRules copies.
func Channels() []Channel {
	out := make([]Channel, len(channels))
	copy(out, channels)
	return out
}
