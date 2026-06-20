package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/johalputt/vayupress/internal/config"
)

// makePNG returns a tiny valid PNG so storeValidatedMedia exercises the real
// decode/optimize/store path rather than a magic-number-only stub.
func makePNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func TestStoreValidatedMediaAcceptsPNGRefusesSVG(t *testing.T) {
	config.Cfg.MediaDir = t.TempDir()

	res, err := storeValidatedMedia(makePNG(t), false)
	if err != nil {
		t.Fatalf("png should be stored, got %v", err)
	}
	if !safeMediaName.MatchString(res.Name) {
		t.Errorf("stored name %q does not match safeMediaName", res.Name)
	}
	if _, statErr := os.Stat(config.Cfg.MediaDir + "/" + res.Name); statErr != nil {
		t.Errorf("stored file missing: %v", statErr)
	}

	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
	if _, err := storeValidatedMedia(svg, false); !errors.Is(err, errMediaUnsupported) {
		t.Fatalf("SVG must be refused with errMediaUnsupported, got %v", err)
	}

	if _, err := storeValidatedMedia(nil, false); !errors.Is(err, errMediaEmpty) {
		t.Fatalf("empty must be errMediaEmpty, got %v", err)
	}

	// PDF is allowed only when allowPDF is set.
	pdf := []byte("%PDF-1.4\n...")
	if _, err := storeValidatedMedia(pdf, false); !errors.Is(err, errMediaUnsupported) {
		t.Errorf("PDF must be refused when allowPDF=false, got %v", err)
	}
}

func TestHandleMediaImportRejectsPrivateURL(t *testing.T) {
	config.Cfg.MediaDir = t.TempDir()
	a := &App{}

	body, _ := json.Marshal(map[string]string{"url": "http://169.254.169.254/latest/meta-data/"})
	req := httptest.NewRequest("POST", "/api/v1/admin/media/import", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	a.handleMediaImport(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("metadata URL import: status = %d, want 400", rec.Code)
	}
}

func TestHandleMediaImportRejectsEmptyURL(t *testing.T) {
	config.Cfg.MediaDir = t.TempDir()
	a := &App{}

	body, _ := json.Marshal(map[string]string{"url": "   "})
	req := httptest.NewRequest("POST", "/api/v1/admin/media/import", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	a.handleMediaImport(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty URL: status = %d, want 400", rec.Code)
	}
}
