// SPDX-License-Identifier: Apache-2.0

package main

// handlers_analytics_export.go — VayuAnalytics report export.
//
// A single admin-only endpoint serialises any report to CSV or JSON for
// download. Everything is computed from the local DB; no third-party services
// are involved (sovereign, zero-telemetry). Exports never contain PII — the
// "sessions" report exposes only the non-reversible visitor hash, never an IP
// or User-Agent.

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/analytics"
	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/csvsafe"
	"github.com/johalputt/vayupress/internal/logging"
)

// analyticsExportReports lists the reports the export endpoint understands. It
// is also used by the admin UI to render the export buttons.
var analyticsExportReports = []string{
	"overview", "pages", "referrers", "browsers", "devices",
	"os", "countries", "regions", "cities", "utm", "events", "sessions", "goals", "journey",
}

// handleAnalyticsParquetExport serves GET /os/api/analytics/export-parquet?days=
// — the warehouse Parquet stream (2025 plan Wave 4). Same install-wide caveat
// as the CSV/JSON export: domain-scoped keys are refused.
func (a *App) handleAnalyticsParquetExport(w http.ResponseWriter, r *http.Request) {
	if a.analytics == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "analytics-disabled", "Analytics not initialised", "")
		return
	}
	if ki, isKey := auth.KeyInfoFromContext(r.Context()); isKey && ki.DomainID != "" {
		writeAPIError(w, r, http.StatusForbidden, "key-scope-mismatch",
			"domain-scoped API keys cannot read install-wide exports", "")
		return
	}
	days := queryInt(r, "days", 30)
	if days < 1 || days > 3650 {
		writeAPIError(w, r, http.StatusBadRequest, "validation_error", "days must be 1..3650", "")
		return
	}
	to := time.Now().UTC()
	from := to.AddDate(0, 0, -(days - 1))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=\"vayupress-pageviews-%s-%s.parquet\"",
			from.Format("20060102"), to.Format("20060102")))
	if err := a.analytics.ExportPageviewsParquet(r.Context(), from.Format("2006-01-02"), to.Format("2006-01-02"), w); err != nil {
		logging.LogWarn("vayuanalytics", "parquet export failed: "+err.Error())
	}
}

// handleAnalyticsExport serves GET {view}/export?report=&format=&days=: a
// site's reports from its own page (/os/analytics/export for the primary,
// /os/d/{id}/analytics/export for a hosted site), every site's from Platform
// and from /api/v1/analytics/export.
//
// format is "csv" (default) or "json". The response carries a Content-Disposition
// attachment header so browsers download it as a file.
func (a *App) handleAnalyticsExport(w http.ResponseWriter, r *http.Request) {
	if a.analytics == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "analytics-disabled", "Analytics not initialised", "")
		return
	}
	v := viewOf(r)
	// A domain-scoped key (migration 092) reads its own site alone: the
	// hosted pages' middleware has already held it to its site, and the
	// primary's reports and every site's are not it.
	if ki, isKey := auth.KeyInfoFromContext(r.Context()); isKey && ki.DomainID != "" && !v.hosted {
		writeAPIError(w, r, http.StatusForbidden, "key-scope-mismatch",
			"domain-scoped API keys cannot read install-wide exports", "")
		return
	}
	report := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("report")))
	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format == "" {
		format = "csv"
	}
	if format != "csv" && format != "json" {
		writeAPIError(w, r, http.StatusBadRequest, "validation_error", "format must be csv or json", "")
		return
	}
	days := queryInt(r, "days", 30)

	header, rows, payload, err := a.buildAnalyticsExport(r, v, report, days)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "validation_error", err.Error(), "")
		return
	}

	stamp := time.Now().UTC().Format("20060102")
	filename := fmt.Sprintf("vayuanalytics-%s-%s.%s", report, stamp, format)
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	if format == "json" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(payload)
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	cw := csv.NewWriter(w)
	_ = cw.Write(header)
	for _, row := range rows {
		_ = csvsafe.Write(cw, row)
	}
	cw.Flush()
}

// buildAnalyticsExport returns the CSV header + rows and an equivalent JSON
// payload for the named report. It returns an error for an unknown report.
func (a *App) buildAnalyticsExport(r *http.Request, v analyticsView, report string, days int) (header []string, rows [][]string, payload interface{}, err error) {
	ctx := r.Context()
	switch report {
	case "overview":
		ov, e := a.analytics.OverviewSinceScoped(ctx, v.scope, days)
		if e != nil {
			return nil, nil, nil, e
		}
		header = []string{"metric", "value"}
		// Ladder rule (2025 plan Wave 4): beyond the cutoff, uniques merge
		// daily HLL sketches — O(days) unions instead of a raw-table scan.
		// The sketches are kept for the install, not per site.
		uniques := ov.UniqueVisitors
		if days > analytics.RollupRawCutoffDays && v.all() {
			from := time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
			to := time.Now().UTC().Format("2006-01-02")
			if u64, e := a.analytics.RollupUniques(ctx, from, to); e == nil && u64 > 0 {
				uniques = int(u64)
			}
		}
		rows = [][]string{
			{"unique_visitors", strconv.Itoa(uniques)},
			{"visits", strconv.Itoa(ov.TotalVisits)},
			{"pageviews", strconv.Itoa(ov.TotalPageviews)},
			{"bounce_rate", fmt.Sprintf("%.2f", ov.BounceRate)},
			{"avg_duration", fmt.Sprintf("%.2f", ov.AvgDuration)},
		}
		return header, rows, ov, nil

	case "pages":
		items, e := a.analytics.TopPagesScoped(ctx, v.scope, days, queryInt(r, "limit", 200))
		if e != nil {
			return nil, nil, nil, e
		}
		header = []string{"path", "pageviews", "unique_visitors"}
		for _, it := range items {
			rows = append(rows, []string{it.Path, strconv.Itoa(it.Pageviews), strconv.Itoa(it.UniqueVisitors)})
		}
		return header, rows, items, nil

	case "referrers":
		items, e := a.analytics.TopReferrersScoped(ctx, v.scope, days, queryInt(r, "limit", 200))
		if e != nil {
			return nil, nil, nil, e
		}
		header = []string{"referrer", "count"}
		for _, it := range items {
			rows = append(rows, []string{it.Referrer, strconv.Itoa(it.Count)})
		}
		return header, rows, items, nil

	case "browsers", "devices", "os":
		items, e := audienceFor(ctx, a.analytics, v.scope, report, days)
		if e != nil {
			return nil, nil, nil, e
		}
		header = []string{report, "visitors"}
		for _, it := range items {
			rows = append(rows, []string{it.Label, strconv.Itoa(it.Count)})
		}
		return header, rows, items, nil

	case "countries", "regions", "cities":
		items, e := audienceFor(ctx, a.analytics, v.scope, report, days)
		if e != nil {
			return nil, nil, nil, e
		}
		header = []string{strings.TrimSuffix(report, "s"), "visitors"}
		for _, it := range items {
			rows = append(rows, []string{it.Label, strconv.Itoa(it.Count)})
		}
		return header, rows, items, nil

	case "utm":
		items, e := a.analytics.UTMStatsScoped(ctx, v.scope, days)
		if e != nil {
			return nil, nil, nil, e
		}
		header = []string{"source", "medium", "campaign", "count"}
		for _, it := range items {
			rows = append(rows, []string{it.Source, it.Medium, it.Campaign, strconv.Itoa(it.Count)})
		}
		return header, rows, items, nil

	case "events":
		items, e := a.analytics.CustomEventsScoped(ctx, v.scope, days)
		if e != nil {
			return nil, nil, nil, e
		}
		header = []string{"event", "count"}
		for _, it := range items {
			rows = append(rows, []string{it.Name, strconv.Itoa(it.Count)})
		}
		return header, rows, items, nil

	case "sessions":
		items, e := a.analytics.RecentSessionsScoped(ctx, v.scope, days, queryInt(r, "limit", 500))
		if e != nil {
			return nil, nil, nil, e
		}
		header = []string{"id", "visitor_id", "browser", "os", "device", "created_at", "events"}
		for _, it := range items {
			rows = append(rows, []string{it.ID, it.VisitorID, it.Browser, it.OS, it.Device, it.CreatedAt, strconv.Itoa(it.Events)})
		}
		return header, rows, items, nil

	case "goals":
		items, e := a.analytics.GoalResultsOver(ctx, v.defs, v.scope, days)
		if e != nil {
			return nil, nil, nil, e
		}
		header = []string{"name", "kind", "target", "completions", "unique_visitors", "conversion_rate"}
		for _, it := range items {
			rows = append(rows, []string{it.Name, it.Kind, it.Target, strconv.Itoa(it.Completions), strconv.Itoa(it.UniqueVisitors), fmt.Sprintf("%.2f", it.ConversionRate)})
		}
		return header, rows, items, nil

	case "journey":
		items, e := a.analytics.PathFlowsScoped(ctx, v.scope, days, queryInt(r, "limit", 100))
		if e != nil {
			return nil, nil, nil, e
		}
		header = []string{"from_path", "to_path", "transitions"}
		for _, it := range items {
			rows = append(rows, []string{it.From, it.To, strconv.Itoa(it.Count)})
		}
		return header, rows, items, nil
	}
	return nil, nil, nil, fmt.Errorf("unknown report %q", report)
}

// audienceFor is the audience breakdown a report names, for scope.
func audienceFor(ctx context.Context, store *analytics.Store, scope, report string, days int) ([]analytics.AudienceStat, error) {
	col := map[string]string{"browsers": "browser", "devices": "device", "countries": "country", "regions": "region", "cities": "city"}[report]
	if col == "" {
		col = "os"
	}
	return store.AudienceScoped(ctx, scope, col, days)
}
