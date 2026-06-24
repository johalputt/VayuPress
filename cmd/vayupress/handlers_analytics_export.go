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
)

// analyticsExportReports lists the reports the export endpoint understands. It
// is also used by the admin UI to render the export buttons.
var analyticsExportReports = []string{
	"overview", "pages", "referrers", "browsers", "devices",
	"os", "countries", "regions", "cities", "utm", "events", "sessions", "goals", "journey",
}

// handleAnalyticsExport serves GET /api/v1/analytics/export?report=&format=&days=
//
// format is "csv" (default) or "json". The response carries a Content-Disposition
// attachment header so browsers download it as a file.
func (a *App) handleAnalyticsExport(w http.ResponseWriter, r *http.Request) {
	if a.analytics == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "analytics-disabled", "Analytics not initialised", "")
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

	header, rows, payload, err := a.buildAnalyticsExport(r, report, days)
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
		_ = cw.Write(row)
	}
	cw.Flush()
}

// buildAnalyticsExport returns the CSV header + rows and an equivalent JSON
// payload for the named report. It returns an error for an unknown report.
func (a *App) buildAnalyticsExport(r *http.Request, report string, days int) (header []string, rows [][]string, payload interface{}, err error) {
	ctx := r.Context()
	switch report {
	case "overview":
		ov, e := a.analytics.OverviewSince(ctx, days)
		if e != nil {
			return nil, nil, nil, e
		}
		header = []string{"metric", "value"}
		rows = [][]string{
			{"unique_visitors", strconv.Itoa(ov.UniqueVisitors)},
			{"visits", strconv.Itoa(ov.TotalVisits)},
			{"pageviews", strconv.Itoa(ov.TotalPageviews)},
			{"bounce_rate", fmt.Sprintf("%.2f", ov.BounceRate)},
			{"avg_duration", fmt.Sprintf("%.2f", ov.AvgDuration)},
		}
		return header, rows, ov, nil

	case "pages":
		items, e := a.analytics.TopPages(ctx, days, queryInt(r, "limit", 200))
		if e != nil {
			return nil, nil, nil, e
		}
		header = []string{"path", "pageviews", "unique_visitors"}
		for _, it := range items {
			rows = append(rows, []string{it.Path, strconv.Itoa(it.Pageviews), strconv.Itoa(it.UniqueVisitors)})
		}
		return header, rows, items, nil

	case "referrers":
		items, e := a.analytics.TopReferrers(ctx, days, queryInt(r, "limit", 200))
		if e != nil {
			return nil, nil, nil, e
		}
		header = []string{"referrer", "count"}
		for _, it := range items {
			rows = append(rows, []string{it.Referrer, strconv.Itoa(it.Count)})
		}
		return header, rows, items, nil

	case "browsers", "devices", "os":
		items, e := audienceFor(ctx, a.analytics, report, days)
		if e != nil {
			return nil, nil, nil, e
		}
		header = []string{report, "visitors"}
		for _, it := range items {
			rows = append(rows, []string{it.Label, strconv.Itoa(it.Count)})
		}
		return header, rows, items, nil

	case "countries", "regions", "cities":
		items, e := audienceFor(ctx, a.analytics, report, days)
		if e != nil {
			return nil, nil, nil, e
		}
		header = []string{strings.TrimSuffix(report, "s"), "visitors"}
		for _, it := range items {
			rows = append(rows, []string{it.Label, strconv.Itoa(it.Count)})
		}
		return header, rows, items, nil

	case "utm":
		items, e := a.analytics.UTMStats(ctx, days)
		if e != nil {
			return nil, nil, nil, e
		}
		header = []string{"source", "medium", "campaign", "count"}
		for _, it := range items {
			rows = append(rows, []string{it.Source, it.Medium, it.Campaign, strconv.Itoa(it.Count)})
		}
		return header, rows, items, nil

	case "events":
		items, e := a.analytics.CustomEvents(ctx, days)
		if e != nil {
			return nil, nil, nil, e
		}
		header = []string{"event", "count"}
		for _, it := range items {
			rows = append(rows, []string{it.Name, strconv.Itoa(it.Count)})
		}
		return header, rows, items, nil

	case "sessions":
		items, e := a.analytics.RecentSessions(ctx, days, queryInt(r, "limit", 500))
		if e != nil {
			return nil, nil, nil, e
		}
		header = []string{"id", "visitor_id", "browser", "os", "device", "created_at", "events"}
		for _, it := range items {
			rows = append(rows, []string{it.ID, it.VisitorID, it.Browser, it.OS, it.Device, it.CreatedAt, strconv.Itoa(it.Events)})
		}
		return header, rows, items, nil

	case "goals":
		items, e := a.analytics.GoalResults(ctx, days)
		if e != nil {
			return nil, nil, nil, e
		}
		header = []string{"name", "kind", "target", "completions", "unique_visitors", "conversion_rate"}
		for _, it := range items {
			rows = append(rows, []string{it.Name, it.Kind, it.Target, strconv.Itoa(it.Completions), strconv.Itoa(it.UniqueVisitors), fmt.Sprintf("%.2f", it.ConversionRate)})
		}
		return header, rows, items, nil

	case "journey":
		items, e := a.analytics.PathFlows(ctx, days, queryInt(r, "limit", 100))
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

// audienceFor dispatches to the correct audience breakdown by report name.
func audienceFor(ctx context.Context, store *analytics.Store, report string, days int) ([]analytics.AudienceStat, error) {
	switch report {
	case "browsers":
		return store.Browsers(ctx, days)
	case "devices":
		return store.Devices(ctx, days)
	case "countries":
		return store.Countries(ctx, days)
	case "regions":
		return store.Regions(ctx, days)
	case "cities":
		return store.Cities(ctx, days)
	default:
		return store.OperatingSystems(ctx, days)
	}
}
