// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/logging"
)

// purgeExpiredAnalytics deletes every analytics row older than the retention
// window, in each store that holds one. It is the one place the retention
// promise is kept: the privacy disclosure publishes AnalyticsRetainDays, and
// two tables (the rollups' visitor sketches, revenue's session ids) were left
// out of the separate purges it replaced.
func (a *App) purgeExpiredAnalytics(ctx context.Context) {
	days := config.Cfg.AnalyticsRetainDays
	if a.analytics != nil {
		if n, err := a.analytics.Purge(ctx, days); err == nil && n > 0 {
			logging.LogInfo("analytics", fmt.Sprintf("purged %d aggregate rows older than %dd", n, days))
		}
		if n, err := a.analytics.PurgeOlderThan(ctx, days); err == nil && n > 0 {
			logging.LogInfo("analytics", fmt.Sprintf("purged %d detailed rows older than %dd", n, days))
		}
	}
	if a.vaEngagement != nil {
		if n, err := a.vaEngagement.Purge(ctx, days); err == nil && n > 0 {
			logging.LogInfo("vayuanalytics", fmt.Sprintf("purged %d engagement rows older than %dd", n, days))
		}
	}
}

// startAnalyticsRetention runs the purge two minutes after start and daily
// after that. A daily ticker alone first fired 24 hours after boot, so an
// install restarted more often than once a day (every release restarts it)
// never purged at all.
func (a *App) startAnalyticsRetention(done <-chan struct{}) {
	go func() {
		first := time.NewTimer(2 * time.Minute)
		defer first.Stop()
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-first.C:
			case <-ticker.C:
			}
			a.purgeExpiredAnalytics(context.Background())
		}
	}()
}
