// SPDX-License-Identifier: Apache-2.0

package main

// scheduled_reports.go — weekly analytics digest by email (2025 plan Wave 4).
//
// The operator opts in with VAYU_REPORT_EMAIL=ops@example.com. Every Monday
// 07:00 UTC the process mails one compact digest of the trailing seven days:
// human-vs-bot split per day, traffic-source quality under the k-anonymity
// floor, and p75 Core Web Vitals. It reads ONLY what the engagement store
// already records, sends through the existing mailer, and is silent
// (disabled) unless both the address and SMTP are configured. A failed send
// logs a warning and waits for next week — a report must never become an
// outage.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/email"
	"github.com/johalputt/vayupress/internal/logging"
)

const reportDays = 7

// startScheduledReports launches the digest loop until ctx is cancelled.
func (a *App) startScheduledReports(ctx context.Context) {
	to := strings.TrimSpace(os.Getenv("VAYU_REPORT_EMAIL"))
	if to == "" {
		return
	}
	if a.mailer == nil || !a.mailer.Active() {
		logging.LogWarn("vayuanalytics", "VAYU_REPORT_EMAIL set but no mail sender configured — scheduled reports disabled")
		return
	}
	go func() {
		for {
			now := time.Now().UTC()
			// Next Monday 07:00 UTC, strictly in the future.
			next := now.AddDate(0, 0, 1)
			for next.Weekday() != time.Monday {
				next = next.AddDate(0, 0, 1)
			}
			fire := time.Date(next.Year(), next.Month(), next.Day(), 7, 0, 0, 0, time.UTC)
			select {
			case <-ctx.Done():
				return
			case <-time.After(fire.Sub(now)):
				subject, body := a.buildWeeklyDigest(reportDays)
				err := a.mailer.Send(email.Message{
					To:      to,
					Subject: fmt.Sprintf("VayuPress weekly report — %s", fire.Format("2006-01-02")),
					Text:    subject + "\n\n" + body,
				})
				if err != nil {
					logging.LogWarn("vayuanalytics", "weekly report send failed: "+err.Error())
				} else {
					logging.LogInfo("vayuanalytics", "weekly report sent to "+to)
				}
			}
		}
	}()
}

// buildWeeklyDigest renders the digest body from live store data.
func (a *App) buildWeeklyDigest(days int) (string, string) {
	var b strings.Builder
	b.WriteString("VayuPress weekly analytics\n\n")
	ctx := context.Background()

	if bs, err := a.vaEngagement.BotShare(ctx, days); err == nil && len(bs.Days) > 0 {
		fmt.Fprintf(&b, "Traffic quality: %.2f%% bot (%d of %d recorded requests)\n",
			bs.BotPercent, bs.BotTotal, bs.HumanTotal+bs.BotTotal)
		for _, d := range bs.Days {
			fmt.Fprintf(&b, "  %s  human=%d goodbot=%d ai=%d headless=%d badbot=%d avg_score=%.2f\n",
				d.Date, d.Human, d.GoodBot, d.AIAgent, d.Headless, d.BadBot, d.BotScore)
		}
	} else {
		b.WriteString("Traffic quality: no data yet\n")
	}

	if srcs, err := a.vaEngagement.SourceBreakdown(ctx, days); err == nil && len(srcs) > 0 {
		b.WriteString("\nTop sources (k>=5 visitors):\n")
		for _, s := range srcs {
			fmt.Fprintf(&b, "  %-14s views=%d sessions=%d engaged=%.0f%%\n",
				s.Category, s.Views, s.Sessions, s.EngagementRate*100)
		}
	}

	if wv, err := a.vaEngagement.WebVitalsP75(ctx, days); err == nil && wv.Samples > 0 {
		fmt.Fprintf(&b, "\nReal-user experience p75 (n=%d): LCP=%dms INP=%dms CLS=%.2f\n",
			wv.Samples, wv.P75LCPMs, wv.P75INPMs, float64(wv.P75CLSX100)/100)
	} else {
		b.WriteString("\nReal-user experience: not enough samples yet\n")
	}

	b.WriteString("\n— sent by your VayuPress, on schedule, cookieless\n")
	return "Weekly analytics digest", b.String()
}
