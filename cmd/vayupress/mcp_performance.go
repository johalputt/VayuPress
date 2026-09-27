// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"time"

	"github.com/johalputt/vayupress/internal/apikeys"
	"github.com/johalputt/vayupress/internal/mcp"
	"github.com/johalputt/vayupress/internal/metrics"
)

// registerPerformanceTools lets an assistant read how fast the install is
// answering, and which routes make the figure, from the process itself.
//
// Without it the only reading was a screenshot of the Monitoring page, and a
// screenshot says the p95 is 1451 ms but not why: the same diagnosis that ran
// for a dozen rounds over a certificate would have run again over latency.
func (a *App) registerPerformanceTools(srv *mcp.Server) {
	srv.Register(mcp.Tool{
		Name: "get_performance",
		Description: "How fast this install is answering: the HTTP p95 and p99 of the last 15 minutes, the " +
			"slowest routes in that window (route, requests, p95, time taken together), write-queue and render " +
			"latency, the render-cache hit ratio and uptime. Use it to find what makes a slow reading.",
		InputSchema: objSchema(nil, map[string]any{}),
		Visible:     a.mcpVisible(apikeys.SectionSettings, apikeys.ActionRead),
		Handler: func(_ context.Context, _ json.RawMessage) (string, error) {
			snap := a.getAdminSnapshot()
			return jsonStr(map[string]any{
				"window_minutes": 15,
				"http_p95_ms":    metrics.HTTPLatencyWindow.Percentile(95),
				"http_p99_ms":    metrics.HTTPLatencyWindow.Percentile(99),
				"slowest_routes": metrics.RouteLatency.Slowest(15),
				// These three are since the process started, not over the window.
				"since_boot": map[string]any{
					"write_p99_ms":    snap.WriteP99,
					"render_p99_ms":   snap.RenderP99,
					"cache_hit_ratio": snap.CacheHitRatio,
				},
				"uptime": (time.Duration(snap.UptimeSeconds) * time.Second).String(),
			}), nil
		},
	})
}
