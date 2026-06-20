package imageproc

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func makePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.NRGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestOptimizeDownscalesWidePNG(t *testing.T) {
	raw := makePNG(t, 3200, 1600)
	res, err := Optimize(raw, "png", 1600)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Resized {
		t.Fatal("expected resize for 3200px-wide image")
	}
	if res.Width != 1600 {
		t.Errorf("width = %d, want 1600", res.Width)
	}
	if res.Height != 800 {
		t.Errorf("height = %d, want 800 (aspect preserved)", res.Height)
	}
	// Output must be a valid PNG.
	if _, err := png.Decode(bytes.NewReader(res.Data)); err != nil {
		t.Fatalf("output not valid PNG: %v", err)
	}
}

func TestOptimizeNoopWhenSmall(t *testing.T) {
	raw := makePNG(t, 800, 600)
	res, err := Optimize(raw, "png", 1600)
	if err != nil {
		t.Fatal(err)
	}
	if res.Resized {
		t.Error("should not resize an already-small image")
	}
	if res.Width != 800 || res.Height != 600 {
		t.Errorf("dims = %dx%d, want 800x600", res.Width, res.Height)
	}
}

func TestOptimizeJPEG(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2000, 1000))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	res, err := Optimize(buf.Bytes(), "jpg", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Resized || res.Width != 1000 {
		t.Errorf("expected resize to 1000px, got resized=%v width=%d", res.Resized, res.Width)
	}
	if _, err := jpeg.Decode(bytes.NewReader(res.Data)); err != nil {
		t.Fatalf("output not valid JPEG: %v", err)
	}
}

func TestOptimizePassesThroughGIF(t *testing.T) {
	// GIF/WebP must never be re-encoded; bytes returned unchanged.
	raw := []byte("GIF89a-not-really")
	res, err := Optimize(raw, "gif", 1600)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(res.Data, raw) {
		t.Error("gif bytes should pass through unchanged")
	}
	if res.Resized {
		t.Error("gif should never be marked resized")
	}
}
