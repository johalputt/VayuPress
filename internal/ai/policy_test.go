package ai_test

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/ai"
)

func TestPolicyRedactEmail(t *testing.T) {
	p := ai.DefaultPolicy()
	if err := p.Compile(); err != nil {
		t.Fatal(err)
	}
	out, err := p.Apply("Contact me at alice@example.com for details")
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if strings.Contains(out, "alice@example.com") {
		t.Error("email should be redacted")
	}
}

func TestPolicyBlockAPIKey(t *testing.T) {
	p := ai.DefaultPolicy()
	_ = p.Compile()
	_, err := p.Apply("Use API_KEY=supersecretvalue1234567 to authenticate")
	if err == nil {
		t.Error("expected block for API key pattern")
	}
}

func TestPolicyCheck(t *testing.T) {
	p := ai.DefaultPolicy()
	_ = p.Compile()
	violations := p.Check("email: user@test.com")
	if len(violations) == 0 {
		t.Error("expected violation for email")
	}
}
