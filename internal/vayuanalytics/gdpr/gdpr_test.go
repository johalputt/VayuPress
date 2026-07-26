// SPDX-License-Identifier: Apache-2.0

package gdpr

import (
	"encoding/json"
	"testing"
)

func TestReportPostureIsCompliant(t *testing.T) {
	r := NewReport(365, "", "")
	if r.CookiesUsed || r.PIIStored {
		t.Fatal("report must assert no cookies and no PII")
	}
	if len(r.ThirdPartyServices) != 0 {
		t.Fatal("must declare zero third-party services")
	}
	if r.IPStorage != "hashed_daily_rotating_salt" {
		t.Fatalf("unexpected ip storage: %q", r.IPStorage)
	}
	if r.DataController != "operator_defined" || r.DeletionRequestEndpoint != "operator_defined" {
		t.Fatal("empty controller/endpoint should default to operator_defined")
	}
}

func TestReportJSONShape(t *testing.T) {
	b, err := NewReport(90, "Example Ltd", "https://example.com/privacy/delete").JSON()
	if err != nil {
		t.Fatalf("json: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["analytics_system"] != "VayuAnalytics" {
		t.Fatalf("bad system field: %v", m["analytics_system"])
	}
	if m["data_retention_days"].(float64) != 90 {
		t.Fatalf("bad retention: %v", m["data_retention_days"])
	}
}
