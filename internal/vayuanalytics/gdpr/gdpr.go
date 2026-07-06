// Package gdpr produces VayuAnalytics's machine-readable privacy disclosure and
// documents the compliance posture in one place. VayuAnalytics is compliant by
// architecture: no cookies for tracking, no PII stored, IPs used only for an
// in-process hash with a daily-rotating salt, no third-party services, and no
// data leaving the server.
package gdpr

import "encoding/json"

// Report is served at /.well-known/privacy-report.json so auditors, browsers
// and privacy tools can verify the analytics posture without reading code.
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
}

// NewReport builds the disclosure with operator-supplied controller/retention.
func NewReport(retentionDays int, dataController, deletionEndpoint string) Report {
	if dataController == "" {
		dataController = "operator_defined"
	}
	if deletionEndpoint == "" {
		deletionEndpoint = "operator_defined"
	}
	return Report{
		AnalyticsSystem:         "VayuAnalytics",
		CookiesUsed:             false,
		PIIStored:               false,
		IPStorage:               "hashed_daily_rotating_salt",
		DataRetentionDays:       retentionDays,
		ThirdPartyServices:      []string{},
		GDPRBasis:               "legitimate_interest_security_and_analytics",
		DataController:          dataController,
		DeletionRequestEndpoint: deletionEndpoint,
	}
}

// JSON renders the report as indented JSON for the well-known endpoint.
func (r Report) JSON() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}
