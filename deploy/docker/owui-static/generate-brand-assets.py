#!/usr/bin/env python3
"""Regenerate every raster brand asset in this directory from one geometry.

Run it through Docker, like every other command in this repository, so it does
not depend on a host Python or a host Pillow:

    docker run --rm -v "$PWD:/workspace" -w /workspace python:3.12-slim \\
      sh -c "pip install --quiet --root-user-action=ignore pillow==10.2.0 \\
             && python3 deploy/docker/owui-static/generate-brand-assets.py"

The Pillow version is pinned because the output is committed: an unpinned
resampling or encoder change would show up as a diff in every PNG for no reason
anyone could explain at review time.

Why this script exists at all: the alternative is a dozen opaque binaries that
no reviewer can check and nobody can regenerate when the brand moves again.
Every PNG and the ICO come from the constants below, so a palette change is a
one line edit plus a re-run, and `favicon.svg` beside this file is the same
geometry expressed as vector.

BRAND SOURCE. Values are copied from the live S Cubed site's own stylesheet,
`src/app/(frontend)/styles.css` in the separate repository
`scubedcombd/scubed`, which is what serves https://scubed.co/hive. They are not
sampled from a screenshot. The hexagon is that site's Hive motif, taken from
`src/app/(frontend)/hive/_sections/hive-hero.tsx`, which clips its floating
marks with

    polygon(50% 0, 100% 25%, 100% 75%, 50% 100%, 0 75%, 0 25%)

reproduced exactly by HEX_POINTS below. The earlier forest green mark
(#0d3b2e on #f8f7f4) came from the superseded hive.scubed.co site; neither of
those two colours appears anywhere in the current stylesheet.
"""

from __future__ import annotations

import pathlib
import struct

from PIL import Image, ImageDraw

HERE = pathlib.Path(__file__).parent

# Brand palette, verbatim from scubedcombd/scubed src/app/(frontend)/styles.css.
CHARCOAL = (0x2B, 0x2B, 0x28, 0xFF)
GOLD = (0xD9, 0xA5, 0x3B, 0xFF)
CREAM = (0xF3, 0xEE, 0xE4, 0xFF)

# The site clips its hexagons with the polygon above. Expressed as (x, y)
# fractions of the mark's bounding box, so any render size reuses them.
HEX_POINTS = [(0.5, 0.0), (1.0, 0.25), (1.0, 0.75), (0.5, 1.0), (0.0, 0.75), (0.0, 0.25)]

# Corner radius as a fraction of the tile, matching the S Cubed app icon
# (src/app/icon.svg draws a 32px tile with rx="7").
RADIUS_RATIO = 7 / 32

SS = 4  # supersample factor; PIL polygons are not antialiased on their own

# Frames in favicon.ico. 16 and 32 are what a browser tab actually draws; the
# larger ones cover Windows shortcut and taskbar surfaces.
ICO_SIZES = [16, 24, 32, 48, 64, 128, 256]


def _hexagon(box_x: float, box_y: float, box_w: float, box_h: float):
    return [(box_x + fx * box_w, box_y + fy * box_h) for fx, fy in HEX_POINTS]


def render(size: int, *, tile: tuple, glyph: tuple, rounded: bool = True, inset: float = 0.30) -> Image.Image:
    """One badge: a tile with the Hive hexagon centred on it.

    `inset` is the fraction of the tile left as margin around the hexagon.
    Maskable icons pass a larger inset so the glyph survives a circular crop.
    """
    n = size * SS
    img = Image.new("RGBA", (n, n), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)

    if rounded:
        d.rounded_rectangle([0, 0, n - 1, n - 1], radius=n * RADIUS_RATIO, fill=tile)
    else:
        d.rectangle([0, 0, n - 1, n - 1], fill=tile)

    # The hexagon is taller than it is wide on the source site (58x66), so keep
    # that ratio rather than fitting it to a square.
    hex_h = n * (1 - 2 * inset)
    hex_w = hex_h * (58 / 66)
    d.polygon(_hexagon((n - hex_w) / 2, (n - hex_h) / 2, hex_w, hex_h), fill=glyph)

    return img.resize((size, size), Image.LANCZOS)


LIGHT = {"tile": CHARCOAL, "glyph": GOLD}   # for light surfaces and browser chrome
DARK = {"tile": CREAM, "glyph": CHARCOAL}   # for dark surfaces, where a charcoal tile would vanish


def write_ico(path: pathlib.Path, sizes: list[int]) -> None:
    """Write a multi-resolution ICO.

    Pillow's own ICO writer re-encodes every frame from a single source image
    and caps at 256px; building the container here keeps each frame rendered at
    its own size, which is what keeps the 16px frame legible.
    """
    frames = [render(s, **LIGHT) for s in sizes]
    blobs = []
    for frame, s in zip(frames, sizes):
        import io

        buf = io.BytesIO()
        frame.save(buf, format="PNG")
        blobs.append(buf.getvalue())

    offset = 6 + 16 * len(blobs)
    out = struct.pack("<HHH", 0, 1, len(blobs))
    for s, blob in zip(sizes, blobs):
        out += struct.pack("<BBBBHHII", s if s < 256 else 0, s if s < 256 else 0, 0, 0, 1, 32, len(blob), offset)
        offset += len(blob)
    path.write_bytes(out + b"".join(blobs))


def main() -> None:
    # Sizes are the ones each consumer actually asks for. favicon-96x96.png was
    # a 128px image and apple-touch-icon.png a 256px one, both of which the
    # index.html link tags declare at other sizes.
    render(256, **LIGHT).save(HERE / "favicon.png")
    render(96, **LIGHT).save(HERE / "favicon-96x96.png")
    render(180, **LIGHT).save(HERE / "apple-touch-icon.png")
    render(256, **LIGHT).save(HERE / "user.png")
    render(512, **LIGHT).save(HERE / "splash.png")

    # Open WebUI swaps to these two on a dark document. A charcoal tile on a
    # near black page is nearly invisible, so the dark pair inverts to a cream
    # tile and keeps the glyph readable.
    render(256, **DARK).save(HERE / "favicon-dark.png")
    render(512, **DARK).save(HERE / "splash-dark.png")

    # manifest.json declares logo.png at 500x500; it was actually 256x256, and a
    # declared/actual mismatch is grounds for a browser to reject the icon and
    # substitute one of its own.
    render(500, **LIGHT).save(HERE / "logo.png")

    # Maskable PWA icons: platforms crop to a circle, so the glyph needs a
    # bigger margin and the tile has to be full bleed rather than rounded.
    render(192, **LIGHT, rounded=False, inset=0.34).save(HERE / "web-app-manifest-192x192.png")
    render(512, **LIGHT, rounded=False, inset=0.34).save(HERE / "web-app-manifest-512x512.png")

    write_ico(HERE / "favicon.ico", ICO_SIZES)

    # Self check: every file this script claims to write exists, is exactly the
    # size its consumer promises, and actually decodes. Image.open() reads only
    # the header, so load() is what proves the pixel data is not truncated;
    # without it a corrupt file passes a size assertion happily.
    expected = {
        "favicon.png": 256, "favicon-96x96.png": 96, "apple-touch-icon.png": 180,
        "user.png": 256, "splash.png": 512, "favicon-dark.png": 256,
        "splash-dark.png": 512, "logo.png": 500,
        "web-app-manifest-192x192.png": 192, "web-app-manifest-512x512.png": 512,
    }
    for name, want in expected.items():
        with Image.open(HERE / name) as im:
            assert im.size == (want, want), f"{name} is {im.size}, expected {want}x{want}"
            im.load()

    # Pillow hands back the largest ICO frame by default, so opening the file and
    # checking one size would miss a missing or corrupt smaller frame, which is
    # exactly the frame a browser tab renders. Walk every frame instead.
    with Image.open(HERE / "favicon.ico") as ico:
        got = sorted(w for w, _ in ico.ico.sizes())
        assert got == ICO_SIZES, f"favicon.ico has frames {got}, expected {ICO_SIZES}"
        for size in ICO_SIZES:
            frame = ico.ico.getimage((size, size))
            assert frame.size == (size, size), f"favicon.ico {size}px frame is {frame.size}"
            frame.load()

    print(f"wrote {len(expected) + 1} brand assets; sizes and every ICO frame verified")


if __name__ == "__main__":
    main()
