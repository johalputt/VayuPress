// Command webhook-notify is an example VayuPress sandbox plugin that POSTs a
// compact JSON notification to an external URL whenever an article hook fires.
//
// Unlike the wordcount example, this plugin needs the network, so its manifest
// MUST declare AllowNetwork: true. The host enforces the sandbox independently;
// the plugin ALSO self-checks the granted capability and refuses to call out if
// network access was not actually granted — defense in depth.
//
// Build:
//
//	go build -o webhook-notify ./docs/plugins/examples/webhook-notify
//
// Register (host side):
//
//	m := sandbox.Manifest{
//	    Name:         "webhook-notify",
//	    Executable:   "/opt/vayupress/plugins/webhook-notify",
//	    AllowNetwork: true,
//	    Timeout:      5 * time.Second,
//	}
//	plugins.RegisterSubprocess(reg, m, "article.created.v1", 1)
//
// Configure the destination with the WEBHOOK_URL environment variable.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"time"
)

type capabilities struct {
	AllowNetwork bool `json:"allow_network"`
}
type request struct {
	Hook         string                 `json:"hook"`
	Payload      map[string]interface{} `json:"payload"`
	Capabilities capabilities           `json:"capabilities"`
}
type logLine struct {
	Level   string `json:"level"`
	Message string `json:"msg"`
}
type response struct {
	OK       bool      `json:"ok"`
	Error    string    `json:"error,omitempty"`
	LogLines []logLine `json:"log_lines,omitempty"`
}

func main() {
	url := os.Getenv("WEBHOOK_URL")
	client := &http.Client{Timeout: 4 * time.Second}
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	enc := json.NewEncoder(os.Stdout)

	for sc.Scan() {
		var req request
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			_ = enc.Encode(response{OK: false, Error: "bad request: " + err.Error()})
			continue
		}
		if url == "" {
			_ = enc.Encode(response{OK: true, LogLines: []logLine{{Level: "warn", Message: "webhook-notify: WEBHOOK_URL unset; skipping"}}})
			continue
		}
		// Self-enforce the capability the host granted.
		if !req.Capabilities.AllowNetwork {
			_ = enc.Encode(response{OK: false, Error: "network capability not granted"})
			continue
		}
		body, _ := json.Marshal(map[string]interface{}{"hook": req.Hook, "slug": req.Payload["slug"]})
		resp, err := client.Post(url, "application/json", bytes.NewReader(body))
		if err != nil {
			_ = enc.Encode(response{OK: false, Error: "post failed: " + err.Error()})
			continue
		}
		resp.Body.Close()
		_ = enc.Encode(response{OK: true, LogLines: []logLine{{Level: "info", Message: "webhook-notify: delivered " + req.Hook}}})
	}
}
