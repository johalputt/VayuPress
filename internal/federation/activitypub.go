// Package federation implements a minimal ActivityPub server for VayuPress.
// Supports Create, Update, Delete, Follow activities for federated publishing.
package federation

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

const (
	ActivityStreamsContext = "https://www.w3.org/ns/activitystreams"
	ActivityCreate         = "Create"
	ActivityUpdate         = "Update"
	ActivityDelete         = "Delete"
	ActivityFollow         = "Follow"
	ActivityAccept         = "Accept"
)

// Actor represents an ActivityPub actor (person, service, etc.)
type Actor struct {
	Context           string `json:"@context"`
	ID                string `json:"id"`
	Type              string `json:"type"`
	Name              string `json:"name"`
	PreferredUsername string `json:"preferredUsername"`
	Inbox             string `json:"inbox"`
	Outbox            string `json:"outbox"`
	PublicKey         *APKey `json:"publicKey,omitempty"`
}

// APKey is an ActivityPub public key descriptor.
type APKey struct {
	ID           string `json:"id"`
	Owner        string `json:"owner"`
	PublicKeyPem string `json:"publicKeyPem"`
}

// Activity represents an ActivityPub activity envelope.
type Activity struct {
	Context   string      `json:"@context"`
	ID        string      `json:"id"`
	Type      string      `json:"type"`
	Actor     string      `json:"actor"`
	Object    interface{} `json:"object"`
	Published string      `json:"published,omitempty"`
}

// Server is a minimal ActivityPub server.
type Server struct {
	mu        sync.RWMutex
	baseURL   string
	actor     Actor
	inbox     []Activity
	outbox    []Activity
	followers []string

	// keyResolver maps a Signature keyId to the signer's RSA public key (PEM).
	// When non-nil, InboxHandler requires and verifies an HTTP signature on
	// every inbound request; when nil, verification is skipped (the historical
	// behaviour, retained for tests and trusted single-tenant deployments).
	keyResolver func(keyID string) (string, error)
}

// SetKeyResolver enables HTTP-signature enforcement on the inbox. The resolver
// receives the Signature header's keyId and must return the corresponding RSA
// public key in PEM form, or an error if the key is unknown.
func (s *Server) SetKeyResolver(fn func(keyID string) (string, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keyResolver = fn
}

// NewServer creates an ActivityPub server for the given actor.
func NewServer(baseURL, username, displayName string) *Server {
	actorURL := baseURL + "/u/" + username
	return &Server{
		baseURL: baseURL,
		actor: Actor{
			Context:           ActivityStreamsContext,
			ID:                actorURL,
			Type:              "Person",
			Name:              displayName,
			PreferredUsername: username,
			Inbox:             actorURL + "/inbox",
			Outbox:            actorURL + "/outbox",
		},
	}
}

// Publish creates a Create activity and adds it to the outbox.
func (s *Server) Publish(objectID, objectType, content string) Activity {
	s.mu.Lock()
	defer s.mu.Unlock()
	act := Activity{
		Context:   ActivityStreamsContext,
		ID:        s.baseURL + "/activities/" + objectID,
		Type:      ActivityCreate,
		Actor:     s.actor.ID,
		Published: time.Now().UTC().Format(time.RFC3339),
		Object: map[string]interface{}{
			"@context": ActivityStreamsContext,
			"id":       objectID,
			"type":     objectType,
			"content":  content,
		},
	}
	s.outbox = append(s.outbox, act)
	return act
}

// ReceiveActivity processes an incoming activity into the inbox.
func (s *Server) ReceiveActivity(act Activity) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inbox = append(s.inbox, act)
	if act.Type == ActivityFollow {
		s.followers = append(s.followers, act.Actor)
	}
}

// maxInboxBody bounds the inbound activity payload (10 MiB) so a hostile peer
// cannot exhaust memory before signature verification.
const maxInboxBody = 10 << 20

// InboxHandler handles POST /inbox.
func (s *Server) InboxHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxInboxBody))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// When a key resolver is configured, every inbound request must carry a
	// valid HTTP signature from a known actor before it is admitted.
	s.mu.RLock()
	resolver := s.keyResolver
	s.mu.RUnlock()
	if resolver != nil {
		if err := s.verifyInbound(r, body, resolver); err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	var act Activity
	if err := json.Unmarshal(body, &act); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	s.ReceiveActivity(act)
	w.WriteHeader(http.StatusAccepted)
}

// verifyInbound resolves the signing key named in the Signature header and
// verifies the request against it.
func (s *Server) verifyInbound(r *http.Request, body []byte, resolver func(string) (string, error)) error {
	params, err := parseSignatureHeader(r.Header.Get("Signature"))
	if err != nil {
		return err
	}
	pem, err := resolver(params["keyid"])
	if err != nil {
		return ErrSignatureInvalid
	}
	pub, err := ParseRSAPublicKeyPEM(pem)
	if err != nil {
		return ErrSignatureInvalid
	}
	return VerifyRequest(r, body, pub)
}

// OutboxHandler handles GET /outbox.
func (s *Server) OutboxHandler(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	w.Header().Set("Content-Type", "application/activity+json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"@context":     ActivityStreamsContext,
		"id":           s.actor.Outbox,
		"type":         "OrderedCollection",
		"totalItems":   len(s.outbox),
		"orderedItems": s.outbox,
	})
}

// ActorHandler handles GET /u/:username.
func (s *Server) ActorHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/activity+json")
	_ = json.NewEncoder(w).Encode(s.actor)
}

// Followers returns the list of follower actor URLs.
func (s *Server) Followers() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, len(s.followers))
	copy(out, s.followers)
	return out
}

// InboxCount returns the number of activities in the inbox.
func (s *Server) InboxCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.inbox)
}

// WebFinger returns a minimal WebFinger response for the actor.
func (s *Server) WebFinger(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/jrd+json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"subject": fmt.Sprintf("acct:%s@%s", s.actor.PreferredUsername, r.Host),
		"links": []map[string]string{{
			"rel":  "self",
			"type": "application/activity+json",
			"href": s.actor.ID,
		}},
	})
}

// OutboxCount returns the number of activities in the outbox.
func (s *Server) OutboxCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.outbox)
}
