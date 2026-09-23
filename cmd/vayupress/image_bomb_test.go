// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"net/http"
	"runtime"
	"testing"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/settings"
)

// bombPNG is a PNG declaring a w×h bitmap with a few bytes of pixel data: a
// file of under a hundred bytes that asks a decoder for w*h*4 bytes of
// memory. The data chunk matters — without one the decoder stops before it
// allocates, and the seed would prove nothing.
func bombPNG(w, h uint32) []byte {
	var b bytes.Buffer
	b.Write([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a})
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], w)
	binary.BigEndian.PutUint32(ihdr[4:8], h)
	ihdr[8], ihdr[9] = 8, 6 // 8-bit RGBA
	chunk := append([]byte("IHDR"), ihdr...)
	_ = binary.Write(&b, binary.BigEndian, uint32(len(ihdr)))
	b.Write(chunk)
	_ = binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(chunk))
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	_, _ = zw.Write(make([]byte, 64))
	_ = zw.Close()
	idat := append([]byte("IDAT"), z.Bytes()...)
	_ = binary.Write(&b, binary.BigEndian, uint32(z.Len()))
	b.Write(idat)
	_ = binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(idat))
	return b.Bytes()
}

// allocatedBy is how many bytes f allocated.
func allocatedBy(f func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	f()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// An admin — a hosted site's own admin too — uploads a logo of a few dozen
// bytes declaring a 10000×10000 bitmap. Reading its colour, or drawing the
// install's app icon from it, must refuse it from its header. Decoding it asks
// for 400 MB; a larger declaration asks for more than the machine has, and a
// failed allocation of that size is a fatal runtime error, not a panic: it
// ends the process, and every site on the install with it.
func TestALogoDeclaringAHugeBitmapIsRefusedFromItsHeader(t *testing.T) {
	a := siteApp(t)
	d := hostedSite(t, a, "harbour.example")
	a.siteSettings = settings.New(dbpkg.DB)
	mark := map[string]string{
		settings.KeyBrandFavicon:     base64.StdEncoding.EncodeToString(bombPNG(10000, 10000)),
		settings.KeyBrandFaviconType: "image/png",
	}
	for _, sc := range []settings.Scope{settings.ForDomain(d.ID), settings.ForPrimary()} {
		if err := a.siteSettings.SetMany(context.Background(), sc, mark); err != nil {
			t.Fatal(err)
		}
	}
	const budget = 32 << 20

	h := editorRouter(a, d, true)
	var code int
	var reason string
	if n := allocatedBy(func() {
		rec, j := editorCall(t, h, http.MethodGet, "/sd/suggest-accent", nil)
		code, reason = rec.Code, errCode(j)
	}); n > budget {
		t.Errorf("reading the logo's colour allocated %d MiB for a logo of %d bytes", n>>20, len(bombPNG(10000, 10000)))
	}
	if code != http.StatusUnprocessableEntity || reason != "logo-too-large" {
		t.Errorf("a logo declaring a 10000x10000 bitmap: %d %q, want 422 logo-too-large", code, reason)
	}

	if n := allocatedBy(func() { a.customAppIcon(context.Background(), 192, false) }); n > budget {
		t.Errorf("drawing the app icon allocated %d MiB for the same logo", n>>20)
	}
}
