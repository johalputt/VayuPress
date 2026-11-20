// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
)

// A post is announced to IndexNow under its own site's host: a post on
// another site (a video a YouTube site posts) was announced at the main
// site's address, where there is no such post. A main-site post keeps the
// main host.
func TestAPostIsAnnouncedUnderItsOwnSite(t *testing.T) {
	openMigratedDB(t)
	a := newIndexNowTestApp(t, testINKey)
	prev := config.Cfg.Domain
	t.Cleanup(func() { config.Cfg.Domain = prev })
	config.Cfg.Domain = "main.example"
	a.domains = domain.New(dbpkg.DB, dbpkg.RDB)
	if _, err := dbpkg.DB.Exec(`INSERT INTO domains(id,host,site_type,is_primary,status,created_at,updated_at) VALUES('vid','videos.example','blog',0,'active',datetime('now'),datetime('now'))`); err != nil {
		t.Fatal(err)
	}
	for slug, dom := range map[string]string{"a-video": "vid", "an-article": ""} {
		if _, err := dbpkg.DB.Exec(`INSERT INTO articles(id,title,slug,content,status,domain_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
			slug, slug, slug, "x", "published", dom, time.Now().UTC(), time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	var sent []map[string]any
	a.outboundClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		sent = append(sent, body)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
	})}

	for slug, host := range map[string]string{"a-video": "videos.example", "an-article": "main.example"} {
		sent = nil
		if state, detail := a.pingIndexNow(slug); state == "skipped" || state == "failed" {
			t.Fatalf("%s was not announced: %s %s", slug, state, detail)
		}
		if len(sent) != 1 {
			t.Fatalf("%s: %d submissions", slug, len(sent))
		}
		urls, _ := sent[0]["urlList"].([]any)
		if sent[0]["host"] != host || len(urls) != 1 || urls[0] != "https://"+host+"/"+slug || sent[0]["keyLocation"] != "https://"+host+"/"+testINKey+".txt" {
			t.Errorf("%s was announced as %v", slug, sent[0])
		}
	}
}
