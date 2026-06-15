// Command seo-stamp is an example VayuPress sandbox plugin demonstrating
// SETTINGS ACCESS under the capability model.
//
// A sandboxed plugin cannot reach the host database. The host-blessed way to
// give a plugin site settings is to export them to a file (see the theme
// console's "Export JSON", which writes a vayupress-theme.json bundle) and grant
// the plugin a READ path to that file. This plugin:
//
//  1. takes the settings file path as its first argument (Manifest.Args),
//  2. self-checks that the host actually granted a read path covering it
//     (capabilities.allowed_read_paths) — refusing to read otherwise, even
//     though the sandbox would also block it, and
//  3. for each article hook, stamps an SEO summary built from the site's
//     author and keywords back to the host as a structured log line.
//
// It declares NO write or network access: least privilege for a read-only
// settings consumer.
//
// Build:
//
//	go build -o seo-stamp ./docs/plugins/examples/seo-stamp
//
// Register (host side):
//
//	m := sandbox.Manifest{
//	    Name:             "seo-stamp",
//	    Executable:       "/opt/vayupress/plugins/seo-stamp",
//	    Args:             []string{"/opt/vayupress/etc/vayupress-theme.json"},
//	    AllowedReadPaths: []string{"/opt/vayupress/etc/"},
//	    Timeout:          2 * time.Second,
//	}
//	plugins.RegisterSubprocess(reg, m, "article.created.v1", 2)
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// request mirrors the host's sandbox.Request (only the fields we use). The
// capabilities block echoes exactly what the host granted, so the plugin can
// self-check before attempting a privileged operation.
type request struct {
	Hook         string                 `json:"hook"`
	Payload      map[string]interface{} `json:"payload"`
	Capabilities struct {
		AllowNetwork     bool     `json:"allow_network"`
		AllowedReadPaths []string `json:"allowed_read_paths"`
	} `json:"capabilities"`
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

// themeBundle is the shape written by the host's /admin/theme/export endpoint.
type themeBundle struct {
	Version  int               `json:"vayupress_theme"`
	Settings map[string]string `json:"settings"`
}

// grantedRead reports whether path is covered by any granted read-path prefix.
func grantedRead(path string, allowed []string) bool {
	for _, p := range allowed {
		if p != "" && strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

func main() {
	settingsPath := ""
	if len(os.Args) > 1 {
		settingsPath = os.Args[1]
	}

	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	enc := json.NewEncoder(os.Stdout)

	for sc.Scan() {
		var req request
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			_ = enc.Encode(response{OK: false, Error: "bad request: " + err.Error()})
			continue
		}

		if settingsPath == "" {
			_ = enc.Encode(response{OK: false, Error: "no settings path configured (Manifest.Args)"})
			continue
		}
		// Self-check the capability before touching the filesystem. Honest
		// least-privilege: don't even try what wasn't granted.
		if !grantedRead(settingsPath, req.Capabilities.AllowedReadPaths) {
			_ = enc.Encode(response{OK: false, Error: "read capability not granted for " + settingsPath})
			continue
		}

		raw, err := os.ReadFile(settingsPath)
		if err != nil {
			_ = enc.Encode(response{OK: false, Error: "read settings: " + err.Error()})
			continue
		}
		var bundle themeBundle
		if err := json.Unmarshal(raw, &bundle); err != nil || bundle.Version != 1 {
			_ = enc.Encode(response{OK: false, Error: "settings file is not a v1 vayupress-theme bundle"})
			continue
		}

		author := bundle.Settings["site.author"]
		keywords := bundle.Settings["head.keywords"]
		slug, _ := req.Payload["slug"].(string)

		_ = enc.Encode(response{
			OK: true,
			LogLines: []logLine{{
				Level:   "info",
				Message: fmt.Sprintf("seo-stamp: slug=%s author=%q keywords=%q", slug, author, keywords),
			}},
		})
	}
}
