// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"io/fs"
	"os"
	"strings"
	"testing"
)

// The console's mark is the brand's own image, not a redrawing. The white file
// is the mark cut from docs/site/assets/logo-dark.png with every pixel kept;
// the black one is the same alpha in black. The cut holds the whole mark, and
// the console draws these two files and nothing else.
func TestTheConsoleMarkIsTheBrandImage(t *testing.T) {
	f, err := os.Open("../../docs/site/assets/logo-dark.png")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	logo, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	// The mark's box in the logo: everything above the wordmark.
	box := image.Rect(151, 6, 722, 433)

	// Nothing of the drawing lies outside the box above the wordmark. The
	// logo's canvas carries a faint haze (alpha up to 10), so ink is > 20.
	for y := 0; y < box.Max.Y; y++ {
		for x := logo.Bounds().Min.X; x < logo.Bounds().Max.X; x++ {
			if (image.Point{x, y}).In(box) {
				continue
			}
			if a := color.NRGBAModel.Convert(logo.At(x, y)).(color.NRGBA).A; a > 20 {
				t.Fatalf("the mark reaches (%d,%d), outside the cut", x, y)
			}
		}
	}

	read := func(tone string) image.Image {
		t.Helper()
		b, err := fs.ReadFile(embeddedStaticFS, "img/vayupress-mark-"+tone+".png")
		if err != nil {
			t.Fatalf("the %s mark is not in the binary: %v", tone, err)
		}
		img, err := png.Decode(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		if img.Bounds().Dx() != box.Dx() || img.Bounds().Dy() != box.Dy() {
			t.Fatalf("the %s mark is %v, the cut is %dx%d", tone, img.Bounds().Size(), box.Dx(), box.Dy())
		}
		return img
	}
	white, black := read("white"), read("black")
	differ := map[string]int{}
	for y := 0; y < box.Dy(); y++ {
		for x := 0; x < box.Dx(); x++ {
			want := color.NRGBAModel.Convert(logo.At(box.Min.X+x, box.Min.Y+y)).(color.NRGBA)
			if got := color.NRGBAModel.Convert(white.At(x, y)).(color.NRGBA); got != want {
				differ["white"]++
			}
			got := color.NRGBAModel.Convert(black.At(x, y)).(color.NRGBA)
			if got.A != want.A || (got.A > 0 && (got.R|got.G|got.B) != 0) {
				differ["black"]++
			}
		}
	}
	for tone, n := range differ {
		t.Errorf("the %s mark differs from the brand image in %d pixels", tone, n)
	}

	for _, tone := range []string{"white", "black"} {
		if !strings.Contains(saMark(), `/os/static/img/vayupress-mark-`+tone+`.png?v=`) {
			t.Errorf("the console does not draw the %s mark: %s", tone, saMark())
		}
	}
	if strings.Count(saMark(), "<img") != 2 || strings.Contains(saMark(), "<svg") {
		t.Errorf("the console draws something besides the two brand images: %s", saMark())
	}
}
