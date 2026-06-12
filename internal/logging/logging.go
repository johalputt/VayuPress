package logging

import (
	"encoding/json"
	"log"
	"regexp"
	"strings"
	"time"
)

type LogFields struct {
	Level      string `json:"level"`
	Time       string `json:"time"`
	RequestID  string `json:"request_id,omitempty"`
	Method     string `json:"method,omitempty"`
	Path       string `json:"path,omitempty"`
	Status     int    `json:"status,omitempty"`
	LatencyMS  int64  `json:"latency_ms,omitempty"`
	RemoteAddr string `json:"remote_addr,omitempty"`
	UserAgent  string `json:"user_agent,omitempty"`
	Component  string `json:"component,omitempty"`
	Error      string `json:"error,omitempty"`
	Severity   string `json:"severity,omitempty"`
	Msg        string `json:"msg,omitempty"`
}

var SecretRedactRe = regexp.MustCompile(`(?i)(password|api.?key|bearer|secret|token|auth|master.?key)\s*[=:]\s*\S+`)

func LogJSON(f LogFields) {
	if f.Error != "" {
		f.Error = SecretRedactRe.ReplaceAllStringFunc(f.Error, func(m string) string {
			idx := strings.IndexAny(m, "=:")
			if idx < 0 {
				return m
			}
			return m[:idx+1] + "[REDACTED]"
		})
	}
	f.Time = time.Now().UTC().Format(time.RFC3339Nano)
	b, _ := json.Marshal(f)
	log.Println(string(b))
}

func LogInfo(component, msg string) {
	LogJSON(LogFields{Level: "info", Component: component, Msg: msg})
}

func LogError(component, msg, e string) {
	LogJSON(LogFields{Level: "error", Component: component, Msg: msg, Error: e, Severity: "error"})
}
