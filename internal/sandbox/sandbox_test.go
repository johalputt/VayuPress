package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// ── Manifest tests ──────────────────────────────────────────────────────────

func TestManifestValidate(t *testing.T) {
	m := Manifest{}
	if err := m.Validate(); err == nil {
		t.Fatal("expected error for empty manifest")
	}
	m.Name = "test"
	if err := m.Validate(); err == nil {
		t.Fatal("expected error when Executable is missing")
	}
	m.Executable = "/bin/echo"
	if err := m.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestManifestPathChecks(t *testing.T) {
	m := Manifest{
		AllowedReadPaths:  []string{"/var/data/", "/tmp/"},
		AllowedWritePaths: []string{"/tmp/"},
	}
	if !m.AllowsReadPath("/var/data/foo.txt") {
		t.Error("expected read allowed for /var/data/foo.txt")
	}
	if m.AllowsReadPath("/etc/passwd") {
		t.Error("expected read denied for /etc/passwd")
	}
	if !m.AllowsWritePath("/tmp/out.json") {
		t.Error("expected write allowed for /tmp/out.json")
	}
	if m.AllowsWritePath("/var/data/secret") {
		t.Error("expected write denied for /var/data/secret")
	}
}

func TestEffectiveDefaults(t *testing.T) {
	m := Manifest{}
	if m.effectiveTimeout() != DefaultPluginTimeout {
		t.Errorf("expected default timeout %v, got %v", DefaultPluginTimeout, m.effectiveTimeout())
	}
	if m.effectiveMaxRestarts() != DefaultMaxRestarts {
		t.Errorf("expected default max restarts %d, got %d", DefaultMaxRestarts, m.effectiveMaxRestarts())
	}
	m.Timeout = 5 * time.Second
	m.MaxRestarts = 10
	if m.effectiveTimeout() != 5*time.Second {
		t.Error("expected configured timeout")
	}
	if m.effectiveMaxRestarts() != 10 {
		t.Error("expected configured max restarts")
	}
}

// ── IPC serialisation tests ─────────────────────────────────────────────────

func TestMarshalUnmarshalRequest(t *testing.T) {
	req := Request{
		HookName:      "article.created.v1",
		Payload:       map[string]interface{}{"title": "Hello"},
		CorrelationID: "corr-1",
		CausationID:   "caus-1",
		TraceID:       "corr-1",
		Capabilities:  Capabilities{AllowNetwork: true, AllowedReadPaths: []string{"/tmp/"}},
	}
	b, err := marshalRequest(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Request
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.HookName != req.HookName {
		t.Errorf("hook mismatch: got %q want %q", got.HookName, req.HookName)
	}
	if got.CorrelationID != req.CorrelationID {
		t.Error("correlation_id mismatch")
	}
	if !got.Capabilities.AllowNetwork {
		t.Error("allow_network not preserved")
	}
}

func TestUnmarshalResponse(t *testing.T) {
	raw := []byte(`{"ok":true,"log_lines":[{"level":"info","msg":"done"}]}`)
	resp, err := unmarshalResponse(raw)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.OK {
		t.Error("expected ok=true")
	}
	if len(resp.LogLines) != 1 || resp.LogLines[0].Level != "info" {
		t.Error("log_lines not preserved")
	}

	errRaw := []byte(`{"ok":false,"error":"boom"}`)
	resp2, err := unmarshalResponse(errRaw)
	if err != nil {
		t.Fatalf("unmarshal error response: %v", err)
	}
	if resp2.OK || resp2.Error != "boom" {
		t.Error("error response not preserved")
	}
}

// ── SubprocessPlugin integration test ───────────────────────────────────────
// Builds a tiny echo-plugin binary in /tmp, then exercises Invoke end-to-end.

const echoPluginSrc = `package main
import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)
type Req struct{ Hook string ` + "`json:\"hook\"`" + ` }
type Resp struct{
	OK bool ` + "`json:\"ok\"`" + `
	LogLines []struct{Level,Msg string ` + "`json:\"level\" json:\"msg\"`" + `} ` + "`json:\"log_lines,omitempty\"`" + `
}
func main(){
	sc:=bufio.NewScanner(os.Stdin)
	for sc.Scan(){
		var r Req
		json.Unmarshal(sc.Bytes(),&r)
		json.NewEncoder(os.Stdout).Encode(Resp{OK:true})
		_ = fmt.Sprintf("hook=%s",r.Hook)
	}
}
`

func buildEchoPlugin(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte(echoPluginSrc), 0600); err != nil {
		t.Fatalf("write plugin src: %v", err)
	}
	bin := filepath.Join(dir, "echoplugin")
	out, err := exec.Command("go", "build", "-o", bin, src).CombinedOutput()
	if err != nil {
		t.Skipf("cannot build echo plugin (go build): %v\n%s", err, out)
	}
	return bin
}

func TestSubprocessPluginInvoke(t *testing.T) {
	bin := buildEchoPlugin(t)
	m := Manifest{
		Name:        "echo",
		Executable:  bin,
		MaxRestarts: 1,
		Timeout:     2 * time.Second,
	}
	p := NewSubprocessPlugin(m)
	defer p.Shutdown()

	ctx := context.Background()
	if err := p.Invoke(ctx, "article.created.v1", map[string]interface{}{"slug": "hello"}); err != nil {
		t.Fatalf("invoke: %v", err)
	}
	stats := p.Stats()
	if stats.Invocations != 1 {
		t.Errorf("expected 1 invocation, got %d", stats.Invocations)
	}
	if stats.Crashes != 0 {
		t.Errorf("expected 0 crashes, got %d", stats.Crashes)
	}
}

// ── P26 security hardening tests ────────────────────────────────────────────

func TestEnforceCapabilitiesNetworkDenied(t *testing.T) {
	m := Manifest{Name: "test", Executable: "/bin/echo", AllowNetwork: false}
	payload := map[string]interface{}{"url": "http://example.com"}
	err := EnforceCapabilities(m, "some.hook", payload)
	if err == nil {
		t.Fatal("expected error for url key when AllowNetwork=false")
	}
}

func TestEnforceCapabilitiesNetworkAllowed(t *testing.T) {
	m := Manifest{Name: "test", Executable: "/bin/echo", AllowNetwork: true}
	payload := map[string]interface{}{"url": "http://example.com"}
	if err := EnforceCapabilities(m, "some.hook", payload); err != nil {
		t.Fatalf("expected no error when AllowNetwork=true: %v", err)
	}
}

func TestEnforceCapabilitiesPathDenied(t *testing.T) {
	m := Manifest{Name: "test", Executable: "/bin/echo", AllowedReadPaths: []string{"/tmp/"}}
	payload := map[string]interface{}{"path": "/etc/passwd"}
	err := EnforceCapabilities(m, "some.hook", payload)
	if err == nil {
		t.Fatal("expected error for path not in allowed paths")
	}
}

func TestEnforceCapabilitiesPathAllowed(t *testing.T) {
	m := Manifest{Name: "test", Executable: "/bin/echo", AllowedReadPaths: []string{"/tmp/"}}
	payload := map[string]interface{}{"path": "/tmp/data.txt"}
	if err := EnforceCapabilities(m, "some.hook", payload); err != nil {
		t.Fatalf("expected no error for allowed path: %v", err)
	}
}

func TestVerifyExecutableHashCorrect(t *testing.T) {
	// Write a temp file with known content.
	dir := t.TempDir()
	f := filepath.Join(dir, "binary")
	content := []byte("test binary content")
	if err := os.WriteFile(f, content, 0600); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(content)
	expected := hex.EncodeToString(h[:])
	if err := verifyExecutableHash(f, expected); err != nil {
		t.Fatalf("expected no error for correct hash: %v", err)
	}
}

func TestVerifyExecutableHashMismatch(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "binary")
	if err := os.WriteFile(f, []byte("real content"), 0600); err != nil {
		t.Fatal(err)
	}
	err := verifyExecutableHash(f, "0000000000000000000000000000000000000000000000000000000000000000")
	if err == nil {
		t.Fatal("expected error for hash mismatch")
	}
}

func TestEffectiveMaxMessageBytes(t *testing.T) {
	m := Manifest{}
	if got := m.effectiveMaxMessageBytes(); got != 1<<20 {
		t.Errorf("expected 1 MiB default, got %d", got)
	}
	m.MaxMessageBytes = 512 * 1024
	if got := m.effectiveMaxMessageBytes(); got != 512*1024 {
		t.Errorf("expected 512 KiB, got %d", got)
	}
}

func TestSubprocessPluginQuarantine(t *testing.T) {
	m := Manifest{
		Name:        "crash-plugin",
		Executable:  "/bin/false", // exits immediately with failure
		MaxRestarts: 2,
		Timeout:     500 * time.Millisecond,
	}
	p := NewSubprocessPlugin(m)
	defer p.Shutdown()

	ctx := context.Background()
	var lastErr error
	// Keep invoking until quarantined.
	for i := 0; i < 10; i++ {
		lastErr = p.Invoke(ctx, "any.hook", nil)
		if lastErr == ErrQuarantined {
			break
		}
	}
	if lastErr != ErrQuarantined {
		t.Fatalf("expected ErrQuarantined after repeated crashes, got: %v", lastErr)
	}
	stats := p.Stats()
	if !stats.Quarantined {
		t.Error("expected quarantined=true in stats")
	}
}
