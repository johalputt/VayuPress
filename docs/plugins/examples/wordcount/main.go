// Command wordcount is an example VayuPress sandbox plugin.
//
// It implements the host IPC protocol: read one JSON Request per line from
// stdin, write one JSON Response per line to stdout. For each article hook it
// counts the words in the article content and reports the total back to the
// host as a structured log line. It declares NO filesystem or network access,
// so it is a good template for a pure, side-effect-free transform.
//
// Build:
//
//	go build -o wordcount ./docs/plugins/examples/wordcount
//
// Register (host side, e.g. in your plugin wiring):
//
//	m := sandbox.Manifest{
//	    Name:       "wordcount",
//	    Executable: "/opt/vayupress/plugins/wordcount",
//	    Timeout:    2 * time.Second,
//	    // No AllowedReadPaths / AllowedWritePaths / AllowNetwork: fully isolated.
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

// request mirrors the host's sandbox.Request (only the fields we use).
type request struct {
	Hook    string                 `json:"hook"`
	Payload map[string]interface{} `json:"payload"`
}

// logLine / response mirror the host's sandbox.Response.
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
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	enc := json.NewEncoder(os.Stdout)
	for sc.Scan() {
		var req request
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			_ = enc.Encode(response{OK: false, Error: "bad request: " + err.Error()})
			continue
		}
		content, _ := req.Payload["content"].(string)
		slug, _ := req.Payload["slug"].(string)
		words := len(strings.Fields(content))
		_ = enc.Encode(response{
			OK: true,
			LogLines: []logLine{{
				Level:   "info",
				Message: fmt.Sprintf("wordcount: hook=%s slug=%s words=%d", req.Hook, slug, words),
			}},
		})
	}
}
