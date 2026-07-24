// Package anonaudit produces an honest, verifiable report of a VayuPress
// install's Tor-Space anonymity posture (ADR-0141): which anti-leak controls are
// live, which residual risks remain, and — deliberately — the things software
// CANNOT do for the operator.
//
// It exists so an operator can SEE that their home-server IP is protected rather
// than take it on faith. It never claims "100% anonymous": anonymity is a
// property of the whole system (Tor, the network, and operator behaviour), not a
// checkbox, and a false guarantee would put a user at real risk.
package anonaudit

// Status is a single control's verdict.
type Status int

const (
	// Pass — the control is active and protecting the operator.
	Pass Status = iota
	// Warn — a residual risk the operator should understand or act on.
	Warn
	// Info — context or an inherent limitation, not a pass/fail.
	Info
	// Fail — a control that SHOULD be active in this mode is not: a real
	// deanonymisation risk.
	Fail
)

// Check is one line of the report.
type Check struct {
	Title  string
	Status Status
	Detail string
}

// Inputs is the live state the report is computed from — passed in so the report
// is pure and testable.
type Inputs struct {
	// OnionMode is whether this install is a Tor Space (VAYUOS_MODE=tor).
	OnionMode bool
	// ClearnetEgressBlocked is safefetch.ClearnetBlocked() — the live guard flag.
	ClearnetEgressBlocked bool
	// LoopbackBind is whether the HTTP listener binds loopback only.
	LoopbackBind bool
	// ExternalSMTPConfigured is whether a non-loopback SMTP relay host is set.
	ExternalSMTPConfigured bool
	// ClearnetDomainSet is whether a real (non-localhost) clearnet Domain is set.
	ClearnetDomainSet bool
}

// Run computes the anonymity report for the given inputs.
func Run(in Inputs) []Check {
	if !in.OnionMode {
		return []Check{{
			Title:  "This install is a Clearnet Space",
			Status: Info,
			Detail: "Anonymity controls apply to a Tor Space (VAYUOS_MODE=tor). A clearnet install is served on your domain over HTTPS and is not anonymous by design.",
		}}
	}

	checks := []Check{
		{
			Title:  "Tor Space engaged",
			Status: Pass,
			Detail: "VAYUOS_MODE=tor — this world is served only as a .onion; there is no clearnet domain or CA-TLS.",
		},
	}

	// The core IP-protection control.
	if in.ClearnetEgressBlocked {
		checks = append(checks, Check{
			Title:  "Clearnet egress disabled",
			Status: Pass,
			Detail: "Every outbound connection (AI, payments, social, webhooks, update checks, external SMTP, remote images) is refused, so no third party is dialed from your real IP.",
		})
	} else {
		checks = append(checks, Check{
			Title:  "Clearnet egress NOT disabled",
			Status: Fail,
			Detail: "The egress guard is not engaged while in Tor mode — outbound connections could reveal your server's real IP. Restart the service; if this persists it is a bug.",
		})
	}

	if in.LoopbackBind {
		checks = append(checks, Check{
			Title:  "HTTP bound to loopback only",
			Status: Pass,
			Detail: "The site is reachable only through Tor's hidden-service port (127.0.0.1); a port scan of the host's public IP serves nothing.",
		})
	} else {
		checks = append(checks, Check{
			Title:  "HTTP not bound to loopback",
			Status: Fail,
			Detail: "The listener is exposed beyond loopback, so the 'anonymous' site may be reachable — and fingerprintable — on the host's public IP.",
		})
	}

	if in.ExternalSMTPConfigured {
		checks = append(checks, Check{
			Title:  "External SMTP relay is configured",
			Status: Warn,
			Detail: "Outbound mail through that relay is refused in a Tor Space (it would reveal your IP), so transactional mail will not send. Leave SMTP unset and use the built-in onion mail instead.",
		})
	}

	if in.ClearnetDomainSet {
		checks = append(checks, Check{
			Title:  "A clearnet domain is still configured",
			Status: Warn,
			Detail: "A DOMAIN value is set. It is not served (loopback bind + onion primary), but clearing it removes any chance of it appearing in generated links.",
		})
	}

	// Honest, inherent limitations — never a pass/fail.
	checks = append(checks,
		Check{
			Title:  "Reach it with Tor Browser",
			Status: Info,
			Detail: "Open the .onion in Tor Browser. Opening it through a clearnet gateway or a non-Tor client defeats the anonymity for whoever does so.",
		},
		Check{
			Title:  "Content can still identify you",
			Status: Info,
			Detail: "What you publish — writing style, photos with metadata, reused handles, payment details — can deanonymise you regardless of the transport. The server cannot police that.",
		},
		Check{
			Title:  "No tool makes you '100% anonymous'",
			Status: Info,
			Detail: "These controls remove the software's own IP leaks. Real-world anonymity also depends on Tor itself, your network, and your operational habits.",
		},
	)
	return checks
}

// Summary counts the report by status for a one-line posture (e.g. boot log).
func Summary(checks []Check) (pass, warn, fail int) {
	for _, c := range checks {
		switch c.Status {
		case Pass:
			pass++
		case Warn:
			warn++
		case Fail:
			fail++
		}
	}
	return
}
