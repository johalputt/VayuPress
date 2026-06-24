// Package kernel provides the VayuOS subsystem orchestration layer.
//
// This package wires together VayuMail, VayuPGP, DNS, and TLS subsystems
// through a typed event bus and ordered boot sequence. It is the nervous
// system connecting all VayuOS components.
package kernel