// SPDX-License-Identifier: Apache-2.0

// Package gdpr produces VayuAnalytics's machine-readable privacy disclosure and
// documents the compliance posture in one place. VayuAnalytics is compliant by
// architecture: no cookies for tracking, no PII stored, IPs used only for an
// in-process hash with a daily-rotating salt, no third-party services, and no
// data leaving the server.
package gdpr

import "encoding/json"

// Report is served at /.well-known/privacy-report.json so auditors, browsers
// and privacy tools can verify the analytics posture without reading code.
//
// Every field describes what the running system ACTUALLY does — several were
// historically hard-coded aspirations while plaintext IP/UA leaked into server
// logs and recovery rows (audit: "privacy claims vs reality"). The values are
// now derived from live configuration where a claim can drift, and callers
// override them when an opt-in mode changes the truth.
type Report struct {
	AnalyticsSystem         string   `json:"analytics_system"`
	CookiesUsed             bool     `json:"cookies_used"`
	PIIStored               bool     `json:"pii_stored"`
	IPStorage               string   `json:"ip_storage"`
	DataRetentionDays       int      `json:"data_retention_days"`
	ThirdPartyServices      []string `json:"third_party_services"`
	GDPRBasis               string   `json:"gdpr_basis"`
	DataController          string   `json:"data_controller"`
	DeletionRequestEndpoint string   `json:"deletion_request_endpoint"`
	// ServerLogIdentity states what client identity reaches stdout/journald,
	// which sits OUTSIDE every analytics purge job: "omitted_by_default" unless
	// the operator opted into VAYU_DEBUG_REQUESTS=1 plaintext debugging.
	ServerLogIdentity string `json:"server_log_identity"`
	// ShieldTrailRetentionDays mirrors the schedule the daily maintenance cycle
	// actually enforces on the hashed block/challenge trails (floored at 7).
	ShieldTrailRetentionDays int `json:"shield_trail_retention_days"`
}

// NewReport builds the disclosure with operator-supplied controller/retention.
func NewReport(retentionDays int, dataController, deletionEndpoint string) Report {
	if dataController == "" {
		dataController = "operator_defined"
	}
	if deletionEndpoint == "" {
		deletionEndpoint = "operator_defined"
	}
	trailRetain := retentionDays
	if trailRetain < 7 {
		trailRetain = 7
	}
	return Report{
		AnalyticsSystem:          "VayuAnalytics",
		CookiesUsed:              false,
		PIIStored:                false,
		IPStorage:                "hashed_daily_rotating_salt",
		DataRetentionDays:        retentionDays,
		ThirdPartyServices:       []string{},
		GDPRBasis:                "legitimate_interest_security_and_analytics",
		DataController:           dataController,
		DeletionRequestEndpoint:  deletionEndpoint,
		ServerLogIdentity:        "omitted_by_default",
		ShieldTrailRetentionDays: trailRetain,
	}
}

// JSON renders the report as indented JSON for the well-known endpoint.
func (r Report) JSON() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}
