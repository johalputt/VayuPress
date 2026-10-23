// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Retention cannot be measured with an identity that changes daily, and the
// route says so. The report it replaced listed cohorts anyway, and counted
// each visitor "retained" in the week of their only visit.
func TestRetentionSaysItCannotBeMeasured(t *testing.T) {
	rec := httptest.NewRecorder()
	(&App{}).handleAnalyticsRetention(rec, httptest.NewRequest(http.MethodGet, "/api/v1/analytics/retention?weeks=12", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("retention answered %d", rec.Code)
	}
	var got struct {
		Measurable *bool             `json:"measurable"`
		Identity   string            `json:"identity"`
		Reason     string            `json:"reason"`
		Cohorts    []json.RawMessage `json:"cohorts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("retention is not the documented object: %v\n%s", err, rec.Body.String())
	}
	if got.Measurable == nil || *got.Measurable || got.Identity != "daily" || !strings.Contains(got.Reason, "changes every day") {
		t.Errorf("retention does not say it cannot be measured, and why: %s", rec.Body.String())
	}
	if got.Cohorts == nil || len(got.Cohorts) != 0 {
		t.Errorf("retention lists cohorts it cannot measure: %s", rec.Body.String())
	}
}
