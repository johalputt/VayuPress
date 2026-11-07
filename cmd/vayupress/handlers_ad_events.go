// SPDX-License-Identifier: Apache-2.0

package main

// handlers_ad_events.go — how often each site's ads are seen and clicked.
//
// Counted the way page views are: the analytics script (cookieless, no
// identifier) reports the slots a page shows and a click on one, and this
// endpoint takes them under the same rules as /api/v1/analytics/collect
// (Do Not Track, the rate limit, readers only). The site is the host this
// install served, never the body, and only that site's enabled slots are
// counted, so a beacon cannot write into another site's numbers.

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/johalputt/vayupress/internal/ads"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/logging"
)

// adCountsFlushEvery is how often counted impressions and clicks are written.
const adCountsFlushEvery = 30 * time.Second

// maxAdIDsPerBeacon bounds the slots one page view may report.
const maxAdIDsPerBeacon = 16

func (a *App) startAdCounts() {
	a.adCounts = ads.NewCounter(dbpkg.DB)
	go func() {
		t := time.NewTicker(adCountsFlushEvery)
		defer t.Stop()
		for range t.C {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			if err := a.adCounts.Flush(ctx); err != nil {
				logging.LogError("ads", "ad counts not written; kept for the next try", err.Error())
			}
			cancel()
		}
	}()
}

// flushAdCounts writes what the counter holds, for the shutdown.
func (a *App) flushAdCounts(limit time.Duration) {
	if a.adCounts == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	if err := a.adCounts.Flush(ctx); err != nil {
		logging.LogError("main", "ad counts on shutdown", err.Error())
	}
}

// handleAdEvents counts a page's ad impressions ({"seen":[ids]}) or a click on
// one ({"click":id}). It answers 204 whatever it counted.
func (a *App) handleAdEvents(w http.ResponseWriter, r *http.Request) {
	if a.ads == nil || a.adCounts == nil || dntHonored(r) || !analyticsLimiter.allow(loginClientIP(r)) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if a.vayuShield != nil && !countsAsReader(a.vayuShield.Classify(r).Result.ClientType, true) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var in struct {
		Seen  []string `json:"seen"`
		Click string   `json:"click"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&in); err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	site := a.contentScope(r)
	ok, err := a.ads.EnabledIDs(r.Context(), site)
	if err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	now := time.Now()
	counted := map[string]bool{}
	for i, id := range in.Seen {
		if i >= maxAdIDsPerBeacon {
			break
		}
		if ok[id] && !counted[id] {
			counted[id] = true
			a.adCounts.Add(site, id, 1, 0, now)
		}
	}
	if ok[in.Click] {
		a.adCounts.Add(site, in.Click, 0, 1, now)
	}
	w.WriteHeader(http.StatusNoContent)
}

// adEventsJS reports the ad slots a page shows, once, and a click on a link
// inside one. Appended to the analytics script, so it runs where page views
// are counted and nowhere else.
const adEventsJS = `!function(){"use strict";
function post(d){try{var j=JSON.stringify(d);if(navigator.sendBeacon){navigator.sendBeacon("/api/v1/ads/events",new Blob([j],{type:"application/json"}));return}var x=new XMLHttpRequest();x.open("POST","/api/v1/ads/events",true);x.setRequestHeader("Content-Type","application/json");x.send(j)}catch(e){}}
function seen(){var ids=[];document.querySelectorAll("[data-ad-id]").forEach(function(e){ids.push(e.getAttribute("data-ad-id"))});if(ids.length)post({seen:ids.slice(0,16)})}
if(document.readyState==="loading"){document.addEventListener("DOMContentLoaded",seen)}else{seen()}
document.addEventListener("click",function(e){var a=e.target.closest&&e.target.closest("[data-ad-id] a[href]");if(a)post({click:a.closest("[data-ad-id]").getAttribute("data-ad-id")})},true);
}();`
