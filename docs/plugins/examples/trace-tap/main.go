// Command trace-tap is an example VayuPress sandbox plugin demonstrating
// PARTICIPATION IN THE DISTRIBUTED TRACING SUBSTRATE.
//
// Every host→plugin request carries the active correlation, causation, and
// trace IDs (see the host's sandbox.Request). The other examples ignore them;
// this one shows the host-blessed pattern for stitching plugin work into the
// same trace waterfall the rest of the platform emits:
//
//  1. read correlation_id / causation_id / trace_id off the request,
//  2. do its (trivial, side-effect-free) work — here, classify the article by
//     length into a coarse "read tier",
//  3. emit a structured log line that ECHOES those IDs, so the plugin's output
//     is correlatable with the originating write in the host's traces view
//     (GET /api/v1/admin/trace/{correlation_id}) without any shared state.
//
// It declares NO filesystem, network, or write access — the strictest sandbox,
// appropriate for a read-only observability tap.
//
// Build:
//
//	go build -o trace-tap ./docs/plugins/examples/trace-tap
//
// Register (host side):
//
//	m := sandbox.Manifest{
//	    Name:       "trace-tap",
//	    Executable: "/opt/vayupress/plugins/trace-tap",
//	    Timeout:    2 * time.Second,
//	    // Fully isolated: no AllowedReadPaths / AllowedWritePaths / AllowNetwork.
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

// request mirrors the host's sandbox.Request. Unlike the other examples it also
// pulls the trace context fields, which the host populates on every call.
type request struct {
	Hook          string                 `json:"hook"`
	Payload       map[string]interface{} `json:"payload"`
	CorrelationID string                 `json:"correlation_id"`
	CausationID   string                 `json:"causation_id"`
	TraceID       string                 `json:"trace_id"`
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

// readTier classifies an article by word count into a coarse reading tier. This
// stands in for any real per-article analysis a tap might do.
func readTier(words int) string {
	switch {
	case words < 100:
		return "micro"
	case words < 600:
		return "standard"
	case words < 2000:
		return "longform"
	default:
		return "deep-dive"
	}
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

		// Fall back to a visible sentinel rather than an empty field so a missing
		// trace context is obvious in the logs rather than silently blank.
		corr := req.CorrelationID
		if corr == "" {
			corr = "(none)"
		}

		// The echoed correlation_id is what makes this line joinable with the
		// originating write in the host's trace view.
		_ = enc.Encode(response{
			OK: true,
			LogLines: []logLine{{
				Level: "info",
				Message: fmt.Sprintf(
					"trace-tap: hook=%s slug=%s words=%d tier=%s correlation_id=%s causation_id=%s trace_id=%s",
					req.Hook, slug, words, readTier(words), corr, req.CausationID, req.TraceID,
				),
			}},
		})
	}
}
