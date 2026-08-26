// SPDX-License-Identifier: Apache-2.0

package vayushield

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/johalputt/vayupress/internal/vayushield/brain"
)

func TestCampaignKeyExtraction(t *testing.T) {
	mk := func(raw string) *http.Request {
		u, _ := url.Parse(raw)
		return &http.Request{URL: u}
	}
	cases := []struct {
		raw  string
		want string
	}{
		{"https://x.test/?utm_campaign=spring&utm_source=ads", "camp:spring"},
		{"https://x.test/?utm_source=newsletter", "camp:newsletter"},
		{"https://x.test/?utm_medium=cpc", ""},   // neither campaign nor source
		{"https://x.test/", ""},                  // no query at all
		{"https://x.test/?utm_campaign=%20", ""}, // whitespace-only is nothing
	}
	for _, tc := range cases {
		if got := CampaignKey(mk(tc.raw)); got != tc.want {
			t.Fatalf("%s: got %q want %q", tc.raw, got, tc.want)
		}
	}
	long := CampaignKey(mk("https://x.test/?utm_campaign=" + string(make([]byte, 300)) + "zzz"))
	if len(long) > len("camp:")+maxCampaignKeyLen {
		t.Fatalf("campaign key not truncated: %d", len(long))
	}
}

func TestCampaignDeltaFor(t *testing.T) {
	if campaignDeltaFor(ActionAllow) != 0 {
		t.Fatal("Allow must carry no opinion")
	}
	block := campaignDeltaFor(ActionBlock)
	pow := campaignDeltaFor(ActionChallengePoW)
	js := campaignDeltaFor(ActionChallengeJS)
	tarpit := campaignDeltaFor(ActionTarpit)
	if !(block < tarpit && tarpit < pow && pow < js && js < 0) {
		t.Fatalf("delta ordering wrong: block=%v tarpit=%v pow=%v js=%v", block, tarpit, pow, js)
	}
}

func TestCampaignBrainEWMA(t *testing.T) {
	b := brain.New()
	key := "camp:poisoned"
	// Repeated blocks drive the campaign below neutral fast (EWMA accumulates).
	for i := 0; i < 5; i++ {
		b.Observe(key, campaignDeltaFor(ActionBlock))
	}
	if s := b.Standing(key); s >= brain.Neutral {
		t.Fatalf("poisoned campaign standing should be below neutral, got %v", s)
	}
	// An untagged key stays untouched.
	if s := b.Standing("camp:other"); s != brain.Neutral {
		t.Fatalf("untouched campaign moved: %v", s)
	}
}
