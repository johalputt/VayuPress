// Package imageproc provides sovereign, dependency-free image optimization for
// VayuPress editor uploads. It uses only the Go standard library image codecs —
// no libvips, no CGO, no third-party scaling libraries — keeping the binary
// self-contained and the supply chain minimal.
//
// The single exported entry point, Optimize, downscales oversized PNG/JPEG
// images to a sane maximum width using area-averaging (box) resampling, which
// gives clean, alias-free thumbnails for the common "screenshot dragged into the
// editor" case. Animated GIF and WebP are passed through untouched because the
// stdlib cannot re-encode them without losing animation/format fidelity.
package imageproc

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"strconv"
)

// DefaultMaxWidthStr is DefaultMaxWidth rendered as a string, for log messages.
func DefaultMaxWidthStr() string { return strconv.Itoa(DefaultMaxWidth) }

// DefaultMaxWidth is the width above which images are downscaled. 1600px covers
// retina-quality article display while cutting multi-megabyte originals down to
// web-appropriate sizes.
const DefaultMaxWidth = 1600

// jpegQuality balances visual fidelity against file size for re-encoded JPEGs.
const jpegQuality = 82

// Result describes the outcome of Optimize.
type Result struct {
	Data    []byte // optimized bytes (or the original, unchanged, when no-op)
	Width   int
	Height  int
	Resized bool
}

// Optimize decodes raw (a PNG or JPEG), and if it is wider than maxWidth,
// downscales it proportionally and re-encodes it. For formats other than
// png/jpeg, or images already within bounds, it returns the original bytes with
// their measured dimensions and Resized=false.
//
// ext is the canonical extension already determined by the caller's magic-number
// check ("png", "jpg", "gif", "webp"); only "png" and "jpg" are processed.
func Optimize(raw []byte, ext string, maxWidth int) (Result, error) {
	if maxWidth <= 0 {
		maxWidth = DefaultMaxWidth
	}
	if ext != "png" && ext != "jpg" {
		// Still report dimensions when decodable, but never re-encode.
		if cfg, _, err := image.DecodeConfig(bytes.NewReader(raw)); err == nil {
			return Result{Data: raw, Width: cfg.Width, Height: cfg.Height}, nil
		}
		return Result{Data: raw}, nil
	}

	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return Result{}, fmt.Errorf("imageproc: decode: %w", err)
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()

	if w <= maxWidth {
		return Result{Data: raw, Width: w, Height: h}, nil
	}

	newW := maxWidth
	newH := int(float64(h) * float64(newW) / float64(w))
	if newH < 1 {
		newH = 1
	}
	dst := boxDownscale(img, newW, newH)

	out, err := encode(dst, ext)
	if err != nil {
		return Result{}, err
	}
	// Guard against pathological cases where re-encoding grew the file; keep the
	// smaller of the two so we never make an upload worse.
	if len(out) >= len(raw) {
		return Result{Data: raw, Width: w, Height: h}, nil
	}
	return Result{Data: out, Width: newW, Height: newH, Resized: true}, nil
}

// encode serializes img back to the given format.
func encode(img image.Image, ext string) ([]byte, error) {
	var buf bytes.Buffer
	switch ext {
	case "png":
		enc := png.Encoder{CompressionLevel: png.BestCompression}
		if err := enc.Encode(&buf, img); err != nil {
			return nil, fmt.Errorf("imageproc: png encode: %w", err)
		}
	case "jpg":
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
			return nil, fmt.Errorf("imageproc: jpeg encode: %w", err)
		}
	default:
		return nil, fmt.Errorf("imageproc: unsupported encode format %q", ext)
	}
	return buf.Bytes(), nil
}

// boxDownscale resamples src down to dstW×dstH by averaging each source region
// that maps to a destination pixel. This is a high-quality, allocation-light
// approach for downscaling (the only direction we ever resize).
func boxDownscale(src image.Image, dstW, dstH int) image.Image {
	sb := src.Bounds()
	srcW, srcH := sb.Dx(), sb.Dy()
	dst := image.NewNRGBA(image.Rect(0, 0, dstW, dstH))

	for dy := 0; dy < dstH; dy++ {
		sy0 := dy * srcH / dstH
		sy1 := (dy + 1) * srcH / dstH
		if sy1 <= sy0 {
			sy1 = sy0 + 1
		}
		for dx := 0; dx < dstW; dx++ {
			sx0 := dx * srcW / dstW
			sx1 := (dx + 1) * srcW / dstW
			if sx1 <= sx0 {
				sx1 = sx0 + 1
			}
			var rSum, gSum, bSum, aSum uint64
			var n uint64
			for sy := sy0; sy < sy1; sy++ {
				for sx := sx0; sx < sx1; sx++ {
					r, g, b, a := src.At(sb.Min.X+sx, sb.Min.Y+sy).RGBA()
					// RGBA() returns 16-bit values; scale to 8-bit.
					rSum += uint64(r >> 8)
					gSum += uint64(g >> 8)
					bSum += uint64(b >> 8)
					aSum += uint64(a >> 8)
					n++
				}
			}
			if n == 0 {
				n = 1
			}
			i := dst.PixOffset(dx, dy)
			dst.Pix[i+0] = uint8(rSum / n)
			dst.Pix[i+1] = uint8(gSum / n)
			dst.Pix[i+2] = uint8(bSum / n)
			dst.Pix[i+3] = uint8(aSum / n)
		}
	}
	return dst
}
