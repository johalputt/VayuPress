package config

import (
	"os"
	"testing"
)

func TestEnvOr(t *testing.T) {
	os.Setenv("VAYU_TEST_KEY", "testval")
	defer os.Unsetenv("VAYU_TEST_KEY")

	if got := EnvOr("VAYU_TEST_KEY", "default"); got != "testval" {
		t.Fatalf("want testval, got %s", got)
	}
	if got := EnvOr("VAYU_MISSING_KEY", "fallback"); got != "fallback" {
		t.Fatalf("want fallback, got %s", got)
	}
}

func TestGetEnvAsInt(t *testing.T) {
	os.Setenv("VAYU_TEST_INT", "42")
	defer os.Unsetenv("VAYU_TEST_INT")

	if got := GetEnvAsInt("VAYU_TEST_INT", 0); got != 42 {
		t.Fatalf("want 42, got %d", got)
	}
	if got := GetEnvAsInt("VAYU_MISSING_INT", 7); got != 7 {
		t.Fatalf("want 7, got %d", got)
	}
}

func TestGetEnvAsIntInvalid(t *testing.T) {
	os.Setenv("VAYU_BAD_INT", "notanumber")
	defer os.Unsetenv("VAYU_BAD_INT")

	got := GetEnvAsInt("VAYU_BAD_INT", 99)
	if got != 99 {
		t.Fatalf("invalid int should return default 99, got %d", got)
	}
}

func TestLoadDefaults(t *testing.T) {
	os.Setenv("API_KEY", "test-key")
	defer os.Unsetenv("API_KEY")

	Load()
	if Cfg.APIKey != "test-key" {
		t.Fatalf("API key not loaded: got %q", Cfg.APIKey)
	}
	if Cfg.Port == "" {
		t.Fatal("Port should have a default value")
	}
	if Cfg.WorkerCount <= 0 {
		t.Fatalf("WorkerCount should be positive, got %d", Cfg.WorkerCount)
	}
	if Cfg.MaxReplayCount <= 0 {
		t.Fatalf("MaxReplayCount should be positive, got %d", Cfg.MaxReplayCount)
	}
}

func TestConfigVersions(t *testing.T) {
	if ConfigVersion == "" {
		t.Fatal("ConfigVersion should not be empty")
	}
	if MinCompatibleConfigVersion == "" {
		t.Fatal("MinCompatibleConfigVersion should not be empty")
	}
}
