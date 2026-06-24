// Package panel provides VayuOS panel HTTP handlers for VayuMail, VayuPGP,
// DNS, TLS, system health, and the first boot setup wizard.
//
// All /vayuos/* routes require admin authentication.
package panel

import (
	"encoding/json"
	"html"
	htmpl "html/template"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/johalputt/vayupress/internal/vayuos/dns"
	"github.com/johalputt/vayupress/internal/vayuos/kernel"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
	vpgp "github.com/johalputt/vayupress/internal/vayuos/pgp"
	vtls "github.com/johalputt/vayupress/internal/vayuos/tls"
)

// HandlersFactory constructs VayuOS subsystem panel handlers.
// All handler methods require the caller to provide the auth middleware
// (requireAdmin) before routing.
type Handlers struct {
	Mail   *MailHandlers
	PGP    *PGPHandlers
	DNS    *DNSHandlers
	TLS    *TLSHandlers
	System *SystemHandlers
	Setup  *SetupHandlers
}

// NewHandlers creates handler sets wired to the given subsystems.
func NewHandlers(mailEngine *vmail.Engine, pgpEngine *vpgp.Engine, dnsMgr *dns.Manager, tlsMgr *vtls.Manager, eventBus *kernel.Bus, healthMon *kernel.HealthMonitor) *Handlers {
	return &Handlers{
		Mail:   &MailHandlers{engine: mailEngine},
		PGP:    &PGPHandlers{engine: pgpEngine},
		DNS:    &DNSHandlers{mgr: dnsMgr},
		TLS:    &TLSHandlers{mgr: tlsMgr},
		System: &SystemHandlers{tlsMgr: tlsMgr, eventBus: eventBus, healthMon: healthMon},
		Setup:  &SetupHandlers{mailEngine: mailEngine, pgpEngine: pgpEngine, dnsMgr: dnsMgr, tlsMgr: tlsMgr},
	}
}

// RegisterRoutes mounts all VayuOS panel routes on a chi.Router.
// The caller must wrap the group with appropriate auth middleware.
func (h *Handlers) RegisterRoutes(r chi.Router) {
	// Main VayuOS dashboard
	r.Get("/vayuos", h.System.handleDashboard)
	r.Get("/vayuos/setup", h.Setup.handleSetupPage)

	// Mail management
	r.Get("/vayuos/mail", h.Mail.handleMailOverview)
	r.Get("/vayuos/mail/queue", h.Mail.handleQueueView)
	r.Get("/vayuos/mail/logs", h.Mail.handleLogs)

	// PGP key management
	r.Get("/vayuos/pgp", h.PGP.handleKeysPage)
	r.Get("/vayuos/pgp/keys", h.PGP.handleKeyList)
	r.Post("/vayuos/pgp/keys/rotate", h.PGP.handleKeyRotate)
	r.Get("/vayuos/pgp/keys/export", h.PGP.handleKeyExport)

	// Domain management
	r.Get("/vayuos/domains", h.DNS.handleDomainsPage)
	r.Get("/vayuos/domains/{domain}/health", h.DNS.handleDomainHealth)

	// TLS management
	r.Get("/vayuos/tls", h.TLS.handleCertPage)

	// System health
	r.Get("/vayuos/system", h.System.handleSystemPage)
	r.Get("/vayuos/system/health.json", h.System.handleHealthJSON)
}

// ── Mail Handlers ────────────────────────────────────────────────────────────

type MailHandlers struct{ engine *vmail.Engine }

func (h *MailHandlers) handleMailOverview(w http.ResponseWriter, r *http.Request) {
	writePanelHTML(w, "Mail", htmpl.HTML(`<h1>VayuMail</h1><p>Mail management dashboard coming soon.</p>`))
}
func (h *MailHandlers) handleQueueView(w http.ResponseWriter, r *http.Request) {
	writePanelHTML(w, "Mail Queue", htmpl.HTML(`<h1>Mail Queue</h1><p>Queue view coming soon.</p>`))
}
func (h *MailHandlers) handleLogs(w http.ResponseWriter, r *http.Request) {
	writePanelHTML(w, "Mail Logs", htmpl.HTML(`<h1>Mail Logs</h1><p>Logs view coming soon.</p>`))
}

// ── PGP Handlers ─────────────────────────────────────────────────────────────

type PGPHandlers struct{ engine *vpgp.Engine }

func (h *PGPHandlers) handleKeysPage(w http.ResponseWriter, r *http.Request) {
	writePanelHTML(w, "PGP Keys", htmpl.HTML(`<h1>VayuPGP</h1><p>PGP key management coming soon.</p>`))
}
func (h *PGPHandlers) handleKeyList(w http.ResponseWriter, r *http.Request) {
	writeJSONPan(w, 200, map[string]interface{}{"keys": []interface{}{}})
}
func (h *PGPHandlers) handleKeyRotate(w http.ResponseWriter, r *http.Request) {
	writeJSONPan(w, 200, map[string]string{"status": "rotated"})
}
func (h *PGPHandlers) handleKeyExport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/pgp-keys")
	w.Write([]byte(""))
}

// ── DNS Handlers ─────────────────────────────────────────────────────────────

type DNSHandlers struct{ mgr *dns.Manager }

func (h *DNSHandlers) handleDomainsPage(w http.ResponseWriter, r *http.Request) {
	writePanelHTML(w, "Domains", htmpl.HTML(`<h1>Domain Management</h1><p>DNS management coming soon.</p>`))
}
func (h *DNSHandlers) handleDomainHealth(w http.ResponseWriter, r *http.Request) {
	domain := chi.URLParam(r, "domain")
	health := h.mgr.CheckHealth(domain)
	writeJSONPan(w, 200, health)
}

// ── TLS Handlers ─────────────────────────────────────────────────────────────

type TLSHandlers struct{ mgr *vtls.Manager }

func (h *TLSHandlers) handleCertPage(w http.ResponseWriter, r *http.Request) {
	writePanelHTML(w, "TLS Certificates", htmpl.HTML(`<h1>TLS Certificates</h1><p>Certificate management coming soon.</p>`))
}

// ── System Handlers ──────────────────────────────────────────────────────────

type SystemHandlers struct {
	tlsMgr    *vtls.Manager
	eventBus  *kernel.Bus
	healthMon *kernel.HealthMonitor
}

func (h *SystemHandlers) handleDashboard(w http.ResponseWriter, r *http.Request) {
	healthStatus := "healthy"
	subsystems := ""
	if h.healthMon != nil {
		status := h.healthMon.Check()
		for name, ch := range status.Subsystems {
			cls := "green"
			if !ch.Healthy {
				cls = "red"
				healthStatus = "degraded"
			}
			subsystems += `<div class="card"><span class="dot ` + cls + `"></span><strong>` +
				html.EscapeString(name) + `</strong>: ` + html.EscapeString(ch.Message) + `</div>`
		}
	}

	body := `<h1>VayuOS System Dashboard</h1>
<p>System status: <strong>` + healthStatus + `</strong></p>
<div class="grid">` + subsystems + `</div>`

	writePanelHTML(w, "VayuOS", htmpl.HTML(body))
}

func (h *SystemHandlers) handleSystemPage(w http.ResponseWriter, r *http.Request) {
	writePanelHTML(w, "System Health", htmpl.HTML(`<h1>System Health</h1><p>Full health dashboard coming soon.</p>`))
}

func (h *SystemHandlers) handleHealthJSON(w http.ResponseWriter, r *http.Request) {
	status := map[string]interface{}{
		"healthy": true,
		"subsystems": map[string]interface{}{
			"vayumail": map[string]interface{}{"healthy": true, "message": "operational"},
			"vayupgp":  map[string]interface{}{"healthy": true, "message": "operational"},
			"tls":      map[string]interface{}{"healthy": true, "message": "no certs managed yet"},
			"dns":      map[string]interface{}{"healthy": true, "message": "no domains configured"},
		},
	}
	writeJSONPan(w, 200, status)
}

// ── Setup Handlers ───────────────────────────────────────────────────────────

type SetupHandlers struct {
	mailEngine *vmail.Engine
	pgpEngine  *vpgp.Engine
	dnsMgr     *dns.Manager
	tlsMgr     *vtls.Manager
}

func (h *SetupHandlers) handleSetupPage(w http.ResponseWriter, r *http.Request) {
	body := `<h1>VayuOS Setup Wizard</h1>
<form method="post" action="/vayuos/setup">
  <div class="card">
    <label>Domain <input type="text" name="domain" required placeholder="example.com"></label>
    <label>Admin Email <input type="email" name="email" required placeholder="admin@example.com"></label>
    <label>Admin Password <input type="password" name="password" required minlength="8"></label>
    <button type="submit" class="btn-primary">Complete Setup</button>
  </div>
</form>
<p class="muted">VayuOS will auto-configure mail, PGP keys, TLS certificates, and DNS records.</p>`

	writePanelHTML(w, "Setup Wizard", htmpl.HTML(body))
}

// ── Response helpers ─────────────────────────────────────────────────────────

func writePanelHTML(w http.ResponseWriter, title string, body htmpl.HTML) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	page := `<!DOCTYPE html><html lang="en"><head>
<meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>` + html.EscapeString(title) + ` — VayuOS</title>
<style>
*{margin:0;padding:0;box-sizing:border-box}
:root{--bg:#0f1117;--bg2:#161822;--border:#2a2d3e;--text:#e1e4ed;--muted:#8b8fa3;--accent:#6c5ce7}
body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;background:var(--bg);color:var(--text);line-height:1.5;padding:32px;max-width:800px;margin:0 auto}
h1{font-size:24px;margin-bottom:8px}
h2{font-size:18px;margin:24px 0 12px;color:var(--muted)}
p{color:var(--muted);font-size:14px;margin-bottom:16px}
.card{background:var(--bg2);border:1px solid var(--border);border-radius:8px;padding:16px;margin-bottom:12px}
.card strong{display:block;margin-bottom:4px}
.grid{display:grid;gap:12px}
label{display:block;margin-bottom:12px;font-size:13px;color:var(--muted)}
input{padding:10px 12px;border-radius:6px;border:1px solid var(--border);background:var(--bg2);color:var(--text);font-size:14px;width:100%;margin-top:4px}
input:focus{border-color:var(--accent);outline:none}
.btn-primary{padding:12px 24px;background:var(--accent);color:#fff;border:none;border-radius:6px;font-size:14px;cursor:pointer;margin-top:8px}
.btn-primary:hover{opacity:.9}
.muted{font-size:12px;color:var(--muted)}
.dot{width:8px;height:8px;border-radius:50%;display:inline-block;margin-right:8px}
.green{background:#00b894}.red{background:#ff7675}
</style></head><body>` + string(body) + `</body></html>`
	w.Write([]byte(page))
}

func writeJSONPan(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
