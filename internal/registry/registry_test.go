package registry_test

import (
	"testing"

	"github.com/johalputt/vayupress/internal/registry"
)

func TestRegisterAndGet(t *testing.T) {
	r := registry.New()
	m := &registry.PluginMeta{
		Name:    "hello",
		Version: "v1.0.0",
		SHA256:  "abc123def456abc123def456abc123def456abc123def456abc123def456abc1",
	}
	if err := r.Register(m); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, ok := r.Get("hello", "v1.0.0")
	if !ok {
		t.Fatal("Get: not found")
	}
	if got.Name != "hello" {
		t.Errorf("got name %q", got.Name)
	}
}

func TestRegisterValidation(t *testing.T) {
	r := registry.New()
	err := r.Register(&registry.PluginMeta{Name: "x"}) // missing Version, SHA256
	if err == nil {
		t.Error("expected error for missing fields")
	}
}
