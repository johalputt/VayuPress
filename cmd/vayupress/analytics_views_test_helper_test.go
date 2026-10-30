// SPDX-License-Identifier: Apache-2.0

package main

import "context"

// renderAllAnalyticsViews is every view of v's Analytics, one after another:
// what one page held before the page had views, for the tests that check a
// figure wherever it is shown.
func renderAllAnalyticsViews(a *App, ctx context.Context, v analyticsView, host string, days int) string {
	out := ""
	for _, view := range analyticsViews {
		out += a.renderSiteAnalyticsBody(ctx, v, host, days, view.id)
	}
	return out
}
