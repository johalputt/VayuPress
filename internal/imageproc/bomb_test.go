package imageproc

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"strings"
	"testing"
)

// ihdrOnlyPNG builds a valid PNG signature + IHDR chunk declaring w×h. It carries
// no image data, but image.DecodeConfig reads only the header — exactly the
// decompression-bomb shape a tiny file with enormous declared dimensions takes.
func ihdrOnlyPNG(w, h uint32) []byte {
	var b bytes.Buffer
	b.Write([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a})
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], w)
	binary.BigEndian.PutUint32(ihdr[4:8], h)
	ihdr[8] = 8 // bit depth
	ihdr[9] = 6 // colour type: RGBA
	// compression(10)/filter(11)/interlace(12) stay 0
	chunk := append([]byte("IHDR"), ihdr...)
	_ = binary.Write(&b, binary.BigEndian, uint32(13))
	b.Write(chunk)
	_ = binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(chunk))
	return b.Bytes()
}

// TestOptimizeRejectsDecompressionBomb guards audit M11: a tiny PNG declaring
// dimensions beyond the pixel budget must be refused BEFORE image.Decode
// allocates the multi-GB bitmap that would OOM the process.
func TestOptimizeRejectsDecompressionBomb(t *testing.T) {
	// 25000×25000 ≈ 625 MP → ~2.5 GB decoded. Must be rejected.
	if _, err := Optimize(ihdrOnlyPNG(25000, 25000), "png", 1600); err == nil {
		t.Fatal("expected a decompression-bomb rejection for a 25000x25000 image")
	} else if !strings.Contains(err.Error(), "megapixel") {
		t.Fatalf("expected a megapixel-limit error, got %v", err)
	}
	// A modest declared size passes the guard (it then fails later on the missing
	// IDAT, but NOT with the pixel-budget error — proving the guard is not
	// over-broad).
	if _, err := Optimize(ihdrOnlyPNG(64, 64), "png", 1600); err != nil && strings.Contains(err.Error(), "megapixel") {
		t.Fatalf("a 64x64 image must not trip the pixel-budget guard, got %v", err)
	}
}
