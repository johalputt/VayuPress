// SPDX-License-Identifier: Apache-2.0

package main

// vayushield_community.go — the attacker list VayuPress installs share
// (internal/vayushield/community), in this install.
//
// Three switches, each off until the operator turns it on:
//
//   - receive: the "community" feed among Shield's network lists. The hub's
//     list is read through intel like any feed (its delta bound, its cache,
//     its status line), as the abusive kind, and verified against the hub's
//     key, which is pinned on the first read from that hub;
//   - report: the addresses this install banned on its own (sign-in
//     spraying, sign-in-link spraying, SSH) are queued and sent every
//     communityReportEvery, signed by this install's own key;
//   - hub: this install serves /api/v1/community/{report,list,key} for
//     others and lists what enough of them reported.
//
// The install's key and the hub's key are files beside the database, 0600:
// a key in the settings table would travel with every settings export.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"html"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/queue"
	"github.com/johalputt/vayupress/internal/safefetch"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/ui"
	"github.com/johalputt/vayupress/internal/vayushield/community"
	"github.com/johalputt/vayupress/internal/vayushield/intel"
)

const (
	communityFeedID      = "community"
	communityReportEvery = 15 * time.Minute
)

// communityClient sends reports and fetches the hub's key. The list itself
// is fetched by intel's own client.
var communityClient = safefetch.New(safefetch.Options{
	MaxBytes:       64 << 10,
	Timeout:        20 * time.Second,
	AllowedSchemes: []string{"https"},
	UserAgent:      "VayuShield-Community/1.0 (+https://vayupress.com)",
})

// communityPost and communityGet are the hub calls; tests answer them.
var (
	communityPost = func(ctx context.Context, target string, body []byte, sig string) (int, error) {
		res, err := communityClient.PostWith(ctx, target, "application/json", body, http.Header{"X-Vayushield-Signature": {sig}})
		if err != nil {
			return 0, err
		}
		return res.Status, nil
	}
	communityGet = func(ctx context.Context, target string) ([]byte, error) {
		res, err := communityClient.Get(ctx, target)
		if err != nil {
			return nil, err
		}
		if res.Status != http.StatusOK {
			return nil, errors.New("the hub answered " + http.StatusText(res.Status))
		}
		return res.Body, nil
	}
)

// communityQueue holds the addresses banned since the last report.
type communityQueue struct {
	mu    sync.Mutex
	addrs map[string]bool
}

func (a *App) communitySetting(ctx context.Context, k string) string {
	if a.siteSettings == nil {
		return ""
	}
	return a.siteSettings.Get(ctx, settings.ForPrimary(), k)
}

// communityHubURL is the hub's origin, without a trailing slash.
func (a *App) communityHubURL(ctx context.Context) string {
	u := strings.TrimRight(strings.TrimSpace(a.communitySetting(ctx, settings.KeyShieldCommunityHubURL)), "/")
	if u == "" {
		u = "https://johal.in"
	}
	return u
}

// communityFeed is the hub's list as one of Shield's network lists.
func (a *App) communityFeed() intel.Feed {
	return intel.Feed{
		ID:   communityFeedID,
		Name: "Attackers other VayuPress installs reported",
		URL:  a.communityHubURL(context.Background()) + "/api/v1/community/list",
		Kind: intel.KindAbusive,
		Note: "Addresses that at least three VayuPress installs, on three different networks, banned for attacking them within a day, " +
			"listed for a week. The hub (Settings › Shared attacker list) signs the list, and its key is fixed on the first read.",
		Parse: a.parseCommunityList,
	}
}

// shieldFeeds is every network list this build offers.
func (a *App) shieldFeeds() []intel.Feed {
	return append(intel.DefaultFeeds(), a.communityFeed())
}

// parseCommunityList verifies the hub's list against its pinned key,
// pinning it on the first read from this hub.
func (a *App) parseCommunityList(body []byte) ([]netip.Prefix, error) {
	ctx := context.Background()
	hub := a.communityHubURL(ctx)
	pinned := ""
	if f := strings.Fields(a.communitySetting(ctx, settings.KeyShieldCommunityHubKey)); len(f) == 2 && f[0] == hub {
		pinned = f[1]
	}
	if pinned == "" {
		k, err := communityGet(ctx, hub+"/api/v1/community/key")
		if err != nil {
			return nil, err
		}
		pinned = strings.TrimSpace(string(k))
		if p, err := base64.StdEncoding.DecodeString(pinned); err != nil || len(p) != ed25519.PublicKeySize {
			return nil, errors.New("the hub's key is not an Ed25519 key")
		}
		if err := a.siteSettings.SetMany(ctx, settings.ForPrimary(), map[string]string{settings.KeyShieldCommunityHubKey: hub + " " + pinned}); err != nil {
			return nil, err
		}
	}
	pub, _ := base64.StdEncoding.DecodeString(pinned)
	return community.VerifyList(body, pub)
}

// communityKey reads, or makes once, a key kept in a file beside the
// database.
func communityKey(name string) (ed25519.PrivateKey, error) {
	p := filepath.Join(filepath.Dir(config.Cfg.DBPath), name)
	if seed, err := os.ReadFile(p); err == nil && len(seed) == ed25519.SeedSize { //nolint:gosec // a fixed name beside the database
		return ed25519.NewKeyFromSeed(seed), nil
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(p, priv.Seed(), 0o600); err != nil {
		return nil, err
	}
	return priv, nil
}

// noteCommunityBan queues an address this install banned on its own, when
// reporting is on. Never blocks: it is called from the sign-in path.
func (a *App) noteCommunityBan(addr string) {
	if a.community == nil {
		return
	}
	if h, _, err := net.SplitHostPort(addr); err == nil {
		addr = h
	}
	ip, err := netip.ParseAddr(addr)
	if err != nil || !community.Public(ip.Unmap()) {
		return
	}
	q := a.community
	q.mu.Lock()
	if len(q.addrs) < community.MaxAddrs {
		q.addrs[ip.Unmap().String()] = true
	}
	q.mu.Unlock()
}

// reportCommunity sends what was queued. What the hub did not take stays
// queued for the next report, unless the hub refused it for good.
func (a *App) reportCommunity(ctx context.Context, now time.Time) {
	if a.community == nil || a.communitySetting(ctx, settings.KeyShieldCommunityReport) != "on" || safefetch.ClearnetBlocked() {
		return
	}
	q := a.community
	q.mu.Lock()
	addrs := make([]string, 0, len(q.addrs))
	for s := range q.addrs {
		addrs = append(addrs, s)
	}
	q.mu.Unlock()
	if len(addrs) == 0 {
		return
	}
	priv, err := communityKey("vayushield-community.key")
	if err != nil {
		logging.LogError("vayushield", "the shared attacker list: this install's key could not be read or made", err.Error())
		return
	}
	body, sig := community.SignReport(priv, now, addrs)
	status, err := communityPost(ctx, a.communityHubURL(ctx)+"/api/v1/community/report", body, sig)
	switch {
	case err != nil:
		logging.LogWarn("vayushield", "the shared attacker list: the report was not sent: "+err.Error())
		return
	case status == http.StatusTooManyRequests || status >= 500:
		return // the next report carries them
	case status != http.StatusNoContent:
		logging.LogWarn("vayushield", "the shared attacker list: the hub refused the report ("+http.StatusText(status)+")")
	}
	q.mu.Lock()
	for _, s := range addrs {
		delete(q.addrs, s)
	}
	q.mu.Unlock()
}

// watchCommunity starts the queue and the report loop.
func (a *App) watchCommunity() {
	a.community = &communityQueue{addrs: map[string]bool{}}
	go func() {
		t := time.NewTicker(communityReportEvery)
		defer t.Stop()
		for {
			select {
			case <-queue.DoneCh:
				return
			case now := <-t.C:
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				a.reportCommunity(ctx, now)
				cancel()
			}
		}
	}()
}

// communityHub is the hub's store, or nil when this install is not a hub.
func (a *App) communityHub(ctx context.Context) *community.Hub {
	if dbpkg.DB == nil || a.communitySetting(ctx, settings.KeyShieldCommunityHub) != "on" {
		return nil
	}
	return &community.Hub{DB: dbpkg.DB}
}

// handleCommunityReport takes a signed report from another install.
func (a *App) handleCommunityReport(w http.ResponseWriter, r *http.Request) {
	h := a.communityHub(r.Context())
	if h == nil {
		http.NotFound(w, r)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, community.MaxBody+1))
	if err != nil || len(body) > community.MaxBody {
		http.Error(w, "the report is too large", http.StatusRequestEntityTooLarge)
		return
	}
	rep, addrs, err := community.VerifyReport(body, r.Header.Get("X-VayuShield-Signature"), time.Now())
	if err == nil {
		from, perr := netip.ParseAddr(auth.ClientIP(r))
		if perr != nil {
			err = errors.New("the report came from no address")
		} else {
			err = h.Accept(r.Context(), rep, addrs, from.Unmap())
		}
	}
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, community.ErrRemoved):
		http.Error(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, community.ErrTooSoon):
		http.Error(w, err.Error(), http.StatusTooManyRequests)
	case errors.Is(err, community.ErrSignature), errors.Is(err, community.ErrClock), errors.Is(err, community.ErrTooMany):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		logging.LogError("vayushield", "the shared attacker list: a report could not be stored", err.Error())
		http.Error(w, "the report could not be stored", http.StatusServiceUnavailable)
	}
}

// handleCommunityList serves the signed list.
func (a *App) handleCommunityList(w http.ResponseWriter, r *http.Request) {
	h := a.communityHub(r.Context())
	if h == nil {
		http.NotFound(w, r)
		return
	}
	priv, err := communityKey("vayushield-community-hub.key")
	listed, lerr := h.Listed(r.Context())
	if err != nil || lerr != nil {
		http.Error(w, "the list could not be read", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(community.SignList(priv, time.Now(), listed))
}

// handleCommunityKey serves the hub's public key, which a reader pins.
func (a *App) handleCommunityKey(w http.ResponseWriter, r *http.Request) {
	if a.communityHub(r.Context()) == nil {
		http.NotFound(w, r)
		return
	}
	priv, err := communityKey("vayushield-community-hub.key")
	if err != nil {
		http.Error(w, "the key could not be read", http.StatusServiceUnavailable)
		return
	}
	pub, _ := priv.Public().(ed25519.PublicKey)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(base64.StdEncoding.EncodeToString(pub) + "\n"))
}

// handleCommunityRemove removes a reporter, for the hub's operator.
func (a *App) handleCommunityRemove(w http.ResponseWriter, r *http.Request) {
	h := a.communityHub(r.Context())
	if h == nil {
		http.NotFound(w, r)
		return
	}
	key := strings.TrimSpace(r.PostFormValue("key"))
	if err := h.Remove(r.Context(), key); err != nil {
		writeOSHTML(w, r, saCallout("danger", html.EscapeString(err.Error())))
		return
	}
	dbpkg.AuditLog("shield.community.reporter_removed", dbpkg.AuditActor(r), key, "")
	writeOSHTML(w, r, a.communityHubBody(r.Context()))
}

// communityBand is the report and hub switches in the Shield form.
func (a *App) communityBand(ctx context.Context) string {
	var b strings.Builder
	b.WriteString(`<div class="card-title vs-section">Shared attacker list</div>`)
	b.WriteString(`<p class="muted text-sm">VayuPress installs can tell each other who attacked them. An install that reports sends the addresses it banned on its own, for sign-in spraying, sign-in-link spraying or SSH, signed by a key of its own; addresses only, never an account or a page. The hub lists an address once three installs on three different networks reported it within a day, for a week. Reading the list is its own switch, under Published network lists.</p>`)
	b.WriteString(`<div class="vs-feat">`)
	b.WriteString(vsRow("sh_comm_report", "Report attackers to the hub", "Every 15 minutes, the addresses banned since the last report are sent to the hub below. Your own addresses are never banned, so never sent.", a.communitySetting(ctx, settings.KeyShieldCommunityReport) == "on", false))
	// Not vsField: that is a number box, which cannot show or take an address.
	b.WriteString(`<div class="vs-adv vs-adv--open"><div class="vs-field"><label for="sh_comm_hub_url">Hub</label><input class="input" type="url" id="sh_comm_hub_url" name="sh_comm_hub_url" value="` +
		html.EscapeString(a.communityHubURL(ctx)) + `" placeholder="https://johal.in" inputmode="url" autocomplete="off" spellcheck="false"></div></div></div>`)
	b.WriteString(`<div class="vs-feat">`)
	b.WriteString(vsRow("sh_comm_hub", "Run a hub for other installs", "This install takes signed reports at /api/v1/community/report and serves the list it signs at /api/v1/community/list. Reporters show on the Shield page, where any of them can be removed with everything it reported.", a.communitySetting(ctx, settings.KeyShieldCommunityHub) == "on", false))
	b.WriteString(`</div>`)
	return b.String()
}

// postedCommunity reads the band's fields from a Shield settings save. A hub
// URL that is not an https origin keeps the one stored.
func postedCommunity(r *http.Request) map[string]string {
	on := func(field string) string {
		if r.PostFormValue(field) != "" {
			return "on"
		}
		return "off"
	}
	out := map[string]string{
		settings.KeyShieldCommunityReport: on("sh_comm_report"),
		settings.KeyShieldCommunityHub:    on("sh_comm_hub"),
	}
	if u := strings.TrimRight(strings.TrimSpace(r.PostFormValue("sh_comm_hub_url")), "/"); u != "" {
		if p, err := url.Parse(u); err == nil && p.Scheme == "https" && p.Host != "" && (p.Path == "" || p.Path == "/") && p.RawQuery == "" {
			out[settings.KeyShieldCommunityHubURL] = "https://" + p.Host
		}
	}
	return out
}

// communityHubBody is the hub's reporters, for the Shield page.
func (a *App) communityHubBody(ctx context.Context) string {
	h := a.communityHub(ctx)
	if h == nil {
		return string(ui.Empty("shield", "This install is not a hub", "Turn on Run a hub for other installs under Settings to take reports.", ""))
	}
	rs, err := h.Reporters(ctx)
	listed, lerr := h.Listed(ctx)
	if err != nil || lerr != nil {
		return string(ui.Empty("shield", "The hub's record could not be read", "The log says why.", ""))
	}
	var rows [][]ui.HTML
	for _, rp := range rs {
		short := rp.Key
		if len(short) > 12 {
			short = short[:12] + "…"
		}
		state := ui.HTML(`<form hx-post="/os/api/shield/community/remove" hx-target="#vs-body-community" hx-swap="innerHTML" hx-confirm="Remove this reporter and everything it reported?"><input type="hidden" name="key" value="` + html.EscapeString(rp.Key) + `"><button class="btn btn--sm btn--danger" type="submit">Remove</button></form>`)
		if rp.Removed {
			state = ui.Text("Removed")
		}
		rows = append(rows, []ui.HTML{ui.HTML(`<code>` + html.EscapeString(short) + `</code>`), ui.Text(rp.Net),
			ui.Text(config.FormatSite(rp.Last, "2 Jan 15:04")), ui.Text(itoaSafe(rp.Reports)), ui.Text(itoaSafe(rp.Current)), state})
	}
	return `<p class="muted text-sm">` + itoaSafe(len(listed)) + ` address` + plural(len(listed)) + ` listed now, from ` + itoaSafe(len(rs)) + ` reporter` + plural(len(rs)) + `.` +
		string(ui.Tip("A reporter that reports what others do not, or reports your own readers, can be removed: what it reported goes with it, and a listing that leaned on it ends.")) + `</p>` +
		string(ui.Table([]string{"Key", "Network", "Last report", "Reports", "Addresses held", ""}, rows, "No install has reported yet."))
}
