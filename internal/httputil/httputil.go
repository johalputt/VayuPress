// Package httputil provides shared HTTP response/decode helpers used across
// all handler packages. It has no dependencies on application state.
package httputil

import (
	"encoding/json"
	"io"
	"net/http"
)

// ErrorBody is the canonical JSON shape for all API error responses.
type ErrorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
	Docs      string `json:"docs,omitempty"`
}

// WriteJSON serialises v as indented JSON with the given HTTP status code.
func WriteJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(v) //nolint:errcheck
}

// WriteError sends a canonical JSON error response.
func WriteError(w http.ResponseWriter, status int, code, msg, requestID, docsURL string) {
	WriteJSON(w, status, map[string]ErrorBody{
		"error": {Code: code, Message: msg, RequestID: requestID, Docs: docsURL},
	})
}

// DecodeJSON reads and JSON-decodes the request body into v, limiting to 10 MiB.
func DecodeJSON(r *http.Request, v interface{}) error {
	defer r.Body.Close()
	return json.NewDecoder(io.LimitReader(r.Body, 10<<20)).Decode(v)
}
