// SPDX-License-Identifier: Apache-2.0

package vayushield

// campaign.go — campaign memory (2025 plan Wave 4, item 5).
//
// The reputation brain keys on network identity; abusers rotate IPs far faster
// than they rotate the campaign string that drives their traffic. A second
// Brain instance keyed on the request's UTM campaign/source gives the shield a
// memory that survives IP rotation: enforcement against traffic wearing a
// campaign drags THAT campaign's standing down, and reward proofs (solved
// challenges) pull it back up. Difficulty selection reads the worse of the two
// standings, so a poisoned campaign hardens every request wearing it.
//
// The key space is namespaced ("camp:") so a hostile query string can never
// collide with an address key in either brain.

import (
	"net/http"
	"strings"

	"github.com/johalputt/vayupress/internal/vayushield/brain"
)

// campaignDeltas are the per-decision EWMA nudges. Challenges cost less than
// blocks: they are suspicion, not conviction — and RewardProof refunds them
// when solved.
func campaignDeltaFor(a Action) float64 {
	switch a {
	case ActionBlock:
		return -0.5
	case ActionTarpit:
		return -0.4
	case ActionChallengePoW:
		return -0.15
	case ActionChallengeJS:
		return -0.1
	default:
		return 0 // Allow carries no opinion
	}
}

// CampaignKey extracts the campaign identity a request wears: utm_campaign if
// present, else utm_source. Empty when neither is set — untagged traffic has no
// campaign to remember.
func CampaignKey(r *http.Request) string {
	if r == nil || r.URL == nil {
		return ""
	}
	q := r.URL.Query()
	if c := strings.TrimSpace(q.Get("utm_campaign")); c != "" {
		return "camp:" + truncKey(c)
	}
	if s := strings.TrimSpace(q.Get("utm_source")); s != "" {
		return "camp:" + truncKey(s)
	}
	return ""
}

const maxCampaignKeyLen = 128

func truncKey(s string) string {
	if len(s) > maxCampaignKeyLen {
		return s[:maxCampaignKeyLen]
	}
	return s
}

// observeCampaign folds this request's decision into the campaign brain. Pure
// bookkeeping — it never changes the current request's outcome.
func (m *Manager) observeCampaign(r *http.Request, a Action) {
	if m.campaignBrain == nil {
		return
	}
	if d := campaignDeltaFor(a); d != 0 {
		if ck := CampaignKey(r); ck != "" {
			m.campaignBrain.Observe(ck, d)
		}
	}
}

// rewardCampaign refunds the campaign when its traffic proves itself (solved
// challenge). Mirrors RewardProof's role for the IP-keyed brain.
func (m *Manager) rewardCampaign(r *http.Request) {
	if m.campaignBrain == nil {
		return
	}
	if ck := CampaignKey(r); ck != "" {
		m.campaignBrain.Observe(ck, 0.25)
	}
}

// CampaignStanding returns the decayed standing of the campaign this request
// wears (Neutral when untagged or unseen). Exposed for difficulty selection
// and the dashboard.
func (m *Manager) CampaignStanding(r *http.Request) float64 {
	if m.campaignBrain == nil {
		return brain.Neutral
	}
	ck := CampaignKey(r)
	if ck == "" {
		return brain.Neutral
	}
	return m.campaignBrain.Standing(ck)
}
