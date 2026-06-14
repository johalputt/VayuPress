package main

import (
	"net/http/httptest"
	"testing"
)

func TestFaviconHandlersServePNG(t *testing.T) {
	for _, b := range [][]byte{faviconDarkPNG, faviconLightPNG} {
		if len(b) < 100 || string(b[1:4]) != "PNG" {
			t.Fatalf("embedded favicon not a PNG (len=%d)", len(b))
		}
		rec := httptest.NewRecorder()
		servePNG(b)(rec, httptest.NewRequest("GET", "/static/favicon.png", nil))
		if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/png" {
			t.Fatalf("bad response: code=%d ct=%q", rec.Code, rec.Header().Get("Content-Type"))
		}
		if rec.Body.Len() != len(b) {
			t.Fatalf("served %d bytes, want %d", rec.Body.Len(), len(b))
		}
	}
}
