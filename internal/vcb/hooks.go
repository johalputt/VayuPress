package vcb

// hooks.go — the enumerated hook catalogue. Before VCB, hook names were
// free-form strings: Manager.Fire accepts anything, Registry.Register accepts
// anything, and a misspelled subscription simply never fired — no error, no
// log. Worse, the plugin SPEC and examples documented names the host never
// fires ("articles.write", "article.created.v1"), so a plugin built from the
// docs was dead on arrival. This catalogue is the single source of truth: the
// validator refuses a manifest subscribing to anything outside it, and the
// table below is generated into docs/compatibility/vcb.md.
//
// The catalogue covers the PLUGIN hook namespace (plugins.Manager.Fire). The
// sibling namespaces — outbound webhook events and durable outbox event types
// — are separate vocabularies exported further down; they share domain facts
// with plugin hooks but intentionally keep their own (versioned) spellings.

// HookName is an enumerated plugin-hook identifier.
type HookName string

// The canonical plugin hooks — exactly the names the host fires through the
// plugin manager (each anchor is the production Fire site).
const (
	// HookArticleCreate fires after an article is created.
	// Payload keys: "slug" (string), "id" (string).
	HookArticleCreate HookName = "article.create"

	// HookArticleUpdate fires after an article is updated.
	// Payload keys: "slug" (string).
	HookArticleUpdate HookName = "article.update"

	// HookArticleDelete fires after an article is deleted.
	// Payload keys: "slug" (string), "id" (string).
	HookArticleDelete HookName = "article.delete"

	// HookCachePurge fires after an admin cache purge.
	// Payload keys: "purge_type" (string), "slug" (string), "purged_count" (number).
	HookCachePurge HookName = "cache.purge"
)

// HookSpec describes one catalogued hook for validators, docs, and tooling.
type HookSpec struct {
	Name        HookName `json:"name"`
	Description string   `json:"description"`
	PayloadKeys []string `json:"payload_keys"`
}

// AllHooks is the ordered, canonical plugin-hook catalogue.
var AllHooks = []HookSpec{
	{HookArticleCreate, "An article was created.", []string{"slug", "id"}},
	{HookArticleUpdate, "An article was updated.", []string{"slug"}},
	{HookArticleDelete, "An article was deleted.", []string{"slug", "id"}},
	{HookCachePurge, "The page cache was purged from the admin.", []string{"purge_type", "slug", "purged_count"}},
}

// validHooks backs O(1) membership checks.
var validHooks = func() map[HookName]bool {
	m := make(map[HookName]bool, len(AllHooks))
	for _, h := range AllHooks {
		m[h.Name] = true
	}
	return m
}()

// ValidHook reports whether h is a catalogued plugin hook.
func ValidHook(h HookName) bool { return validHooks[h] }

// HookNames returns the catalogue as plain strings, in canonical order.
func HookNames() []string {
	out := make([]string, len(AllHooks))
	for i, h := range AllHooks {
		out[i] = string(h.Name)
	}
	return out
}

// WebhookEvents is the vocabulary of outbound webhook event names a
// subscription may target (plus "*" for all). Webhooks are HMAC-signed HTTP
// POSTs; the event name is delivered in the X-VayuPress-Event header. This is
// a distinct, versioned namespace from plugin hooks.
var WebhookEvents = []string{
	"article.created.v1",
	"article.updated.v1",
	"article.deleted.v1",
	"payment.order_created.v1",
	"payment.completed.v1",
}

// OutboxEventTypes is the vocabulary of durable outbox envelope EventTypes
// (internal transactional events relayed after commit). Documented for
// completeness; extensions do not subscribe to these directly.
var OutboxEventTypes = []string{
	"article.created.v1",
	"article.updated.v1",
	"article.deleted.v1",
	"cache.invalidated.v1",
}
