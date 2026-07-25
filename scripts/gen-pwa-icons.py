#!/usr/bin/env python3
"""Generate the public web-app (PWA) icons from the site's 256x256 brand mark.

Run from the repository root:

    python3 scripts/gen-pwa-icons.py

Writes cmd/vayupress/assets/webapp-{192,512,maskable-512,apple-180}.png, which are
embedded into the binary and served at /static/icons/. Re-run it after changing
favicon-light.png so the app icons stay in step with the brand mark.

It is deliberately dependency-free (stdlib zlib only) so it runs anywhere the repo
does, and it only ever reads 8-bit RGBA non-interlaced PNGs — which is what the
brand mark is. Every transform is exact:

  * 512 is an integer 2x pixel doubling, so it invents no detail that is not in
    the source; it exists so the launcher and splash screen get the size they ask
    for instead of upscaling a 256 themselves.
  * 192 and 180 are area-averaged downscales.
  * The maskable icon is the mark at 62.5% on an opaque brand-coloured canvas.
    Android crops a maskable icon to its own shape (circle, squircle, rounded
    square), so the mark has to sit inside the 80%-diameter safe zone and the
    canvas has to be fully opaque — an unpadded logo declared maskable gets its
    edges clipped, which is what the previous manifest asked for.
"""

import struct
import sys
import zlib
from pathlib import Path

BACKGROUND = (0x0A, 0x0F, 0x1A, 0xFF)  # matches the manifest background_color


def read_png(path):
    """Decode an 8-bit RGBA non-interlaced PNG into (width, height, rows)."""
    raw = path.read_bytes()
    if raw[:8] != b"\x89PNG\r\n\x1a\n":
        raise SystemExit(f"{path}: not a PNG")
    width = height = None
    idat = bytearray()
    pos = 8
    while pos < len(raw):
        (length,) = struct.unpack(">I", raw[pos : pos + 4])
        kind = raw[pos + 4 : pos + 8]
        body = raw[pos + 8 : pos + 8 + length]
        if kind == b"IHDR":
            width, height, depth, colour, _, _, interlace = struct.unpack(">IIBBBBB", body)
            if (depth, colour, interlace) != (8, 6, 0):
                raise SystemExit(f"{path}: need 8-bit RGBA non-interlaced, got {depth}/{colour}/{interlace}")
        elif kind == b"IDAT":
            idat += body
        elif kind == b"IEND":
            break
        pos += 12 + length

    data = zlib.decompress(bytes(idat))
    stride = width * 4
    rows, previous = [], bytearray(stride)
    at = 0
    for _ in range(height):
        filter_type = data[at]
        line = bytearray(data[at + 1 : at + 1 + stride])
        at += 1 + stride
        # Undo the per-scanline filter (PNG spec section 9.2).
        if filter_type == 1:
            for i in range(4, stride):
                line[i] = (line[i] + line[i - 4]) & 0xFF
        elif filter_type == 2:
            for i in range(stride):
                line[i] = (line[i] + previous[i]) & 0xFF
        elif filter_type == 3:
            for i in range(stride):
                left = line[i - 4] if i >= 4 else 0
                line[i] = (line[i] + ((left + previous[i]) >> 1)) & 0xFF
        elif filter_type == 4:
            for i in range(stride):
                a = line[i - 4] if i >= 4 else 0
                b = previous[i]
                c = previous[i - 4] if i >= 4 else 0
                p = a + b - c
                pa, pb, pc = abs(p - a), abs(p - b), abs(p - c)
                pred = a if (pa <= pb and pa <= pc) else (b if pb <= pc else c)
                line[i] = (line[i] + pred) & 0xFF
        elif filter_type != 0:
            raise SystemExit(f"{path}: unknown scanline filter {filter_type}")
        rows.append(line)
        previous = line
    return width, height, rows


def write_png(path, width, height, rows):
    """Encode 8-bit RGBA rows, one unfiltered scanline each."""
    body = bytearray()
    for row in rows:
        body.append(0)
        body += row

    def chunk(kind, payload):
        return (
            struct.pack(">I", len(payload))
            + kind
            + payload
            + struct.pack(">I", zlib.crc32(kind + payload) & 0xFFFFFFFF)
        )

    png = b"\x89PNG\r\n\x1a\n"
    png += chunk(b"IHDR", struct.pack(">IIBBBBB", width, height, 8, 6, 0, 0, 0))
    png += chunk(b"IDAT", zlib.compress(bytes(body), 9))
    png += chunk(b"IEND", b"")
    path.write_bytes(png)
    return len(png)


def scale(width, height, rows, size):
    """Resample to size x size: exact pixel doubling when the ratio is integral,
    area averaging otherwise."""
    out = []
    if size % width == 0 and size % height == 0:
        fx, fy = size // width, size // height
        for y in range(size):
            src = rows[y // fy]
            line = bytearray(size * 4)
            for x in range(size):
                s = (x // fx) * 4
                line[x * 4 : x * 4 + 4] = src[s : s + 4]
            out.append(line)
        return out
    for y in range(size):
        y0, y1 = y * height // size, max(y * height // size + 1, (y + 1) * height // size)
        line = bytearray(size * 4)
        for x in range(size):
            x0, x1 = x * width // size, max(x * width // size + 1, (x + 1) * width // size)
            totals = [0, 0, 0, 0]
            count = 0
            for sy in range(y0, y1):
                row = rows[sy]
                for sx in range(x0, x1):
                    off = sx * 4
                    for c in range(4):
                        totals[c] += row[off + c]
                    count += 1
            for c in range(4):
                line[x * 4 + c] = totals[c] // count
        out.append(line)
    return out


def on_background(size, rows, inset):
    """Composite a centred, scaled mark onto an opaque brand-coloured canvas."""
    mark = scale(size, size, rows, size - 2 * inset)
    canvas = [bytearray(BACKGROUND * size) for _ in range(size)]
    for y, src in enumerate(mark):
        dst = canvas[y + inset]
        for x in range(len(src) // 4):
            r, g, b, a = src[x * 4 : x * 4 + 4]
            off = (x + inset) * 4
            if a == 255:
                dst[off : off + 4] = bytes((r, g, b, 255))
            elif a:
                for c, v in enumerate((r, g, b)):
                    dst[off + c] = (v * a + dst[off + c] * (255 - a)) // 255
    return canvas


def main():
    root = Path(__file__).resolve().parent.parent
    assets = root / "cmd" / "vayupress" / "assets"
    width, height, rows = read_png(assets / "favicon-light.png")
    if width != height:
        raise SystemExit(f"brand mark must be square, got {width}x{height}")

    doubled = scale(width, height, rows, 512)
    for name, pixels, size in (
        ("webapp-192.png", scale(width, height, rows, 192), 192),
        ("webapp-512.png", doubled, 512),
        ("webapp-apple-180.png", on_background(180, rows, 18), 180),
        # 62.5% of the canvas: inside Android's 80%-diameter safe zone with room
        # to spare, so no mask shape can clip the mark.
        ("webapp-maskable-512.png", on_background(512, doubled, 96), 512),
    ):
        written = write_png(assets / name, size, size, pixels)
        print(f"  {name}: {size}x{size}, {written} bytes")


if __name__ == "__main__":
    sys.exit(main())
