package logging

import (
	"strings"
	"testing"
)

func TestSecretRedaction(t *testing.T) {
	cases := []struct {
		input    string
		redacted bool
	}{
		{"password=hunter2", true},
		{"api_key=abc123", true},
		{"bearer=tok123", true},
		{"token=mysecret", true},
		{"normal log message", false},
		{"user logged in", false},
	}
	for _, c := range cases {
		got := SecretRedactRe.MatchString(c.input)
		if got != c.redacted {
			t.Errorf("input %q: want redacted=%v got %v", c.input, c.redacted, got)
		}
	}
}

func TestLogFieldsStringer(t *testing.T) {
	// LogJSON should not panic and should produce JSON-like output.
	// Capture is not straightforward; just ensure no panic.
	LogInfo("test", "hello world")
	LogError("test", "something broke", "err detail")
}

func TestRedactInMessage(t *testing.T) {
	msg := "api_key=supersecret"
	replaced := SecretRedactRe.ReplaceAllString(msg, "[REDACTED]")
	if strings.Contains(replaced, "supersecret") {
		t.Errorf("secret should be redacted in: %q", replaced)
	}
	if !strings.Contains(replaced, "[REDACTED]") {
		t.Errorf("replacement marker missing in: %q", replaced)
	}
}
