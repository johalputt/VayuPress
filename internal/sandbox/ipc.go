package sandbox

import "encoding/json"

// Request is the JSON message sent to a sandboxed plugin process over stdin.
// The plugin reads one Request per line and responds with one Response per line.
type Request struct {
	HookName      string                 `json:"hook"`
	Payload       map[string]interface{} `json:"payload"`
	CorrelationID string                 `json:"correlation_id,omitempty"`
	CausationID   string                 `json:"causation_id,omitempty"`
	TraceID       string                 `json:"trace_id,omitempty"`
	// Capabilities echoes the granted permissions so the plugin can self-check.
	Capabilities Capabilities `json:"capabilities"`
}

// Response is the JSON message a sandboxed plugin writes to stdout per request.
type Response struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	// LogLines lets the plugin emit structured log messages that the host
	// forwards through its own logging pipeline with the request correlation ID.
	LogLines []LogLine `json:"log_lines,omitempty"`
}

// LogLine is a single structured log entry emitted by the plugin subprocess.
type LogLine struct {
	Level   string `json:"level"`
	Message string `json:"msg"`
}

// Capabilities is the permission set granted to a plugin for a single invocation.
// The plugin MAY use this to self-enforce; the HOST enforces it independently.
type Capabilities struct {
	AllowNetwork      bool     `json:"allow_network"`
	AllowedReadPaths  []string `json:"allowed_read_paths,omitempty"`
	AllowedWritePaths []string `json:"allowed_write_paths,omitempty"`
}

// marshalRequest serialises req as a single line of JSON (no trailing newline — caller adds it).
func marshalRequest(req Request) ([]byte, error) {
	return json.Marshal(req)
}

// unmarshalResponse parses a single JSON line into a Response.
func unmarshalResponse(line []byte) (Response, error) {
	var resp Response
	err := json.Unmarshal(line, &resp)
	return resp, err
}
