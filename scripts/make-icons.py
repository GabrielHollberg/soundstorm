#!/usr/bin/env python3
"""Draw SoundStorm's app icons from the cloud in the logo, and its bolt.

The logo arrived as a 500x500 PNG with the cloud only 150 pixels wide, too
small to enlarge into a 512px icon without blur. The cloud is three shapes,
measured off that image: a large circle, a smaller one to its right, and a
rounded bar along the bottom. Drawn from those, every size is sharp.

    python3 scripts/make-icons.py

writes internal/webui/assets/favicon.svg and icons/*.png. Needs Pillow.
"""
from pathlib import Path

from PIL import Image, ImageDraw

ASSETS = Path(__file__).resolve().parent.parent / "internal" / "webui" / "assets"

# The cloud in the logo's own pixels. Its box is x 176.5-324.5, y 177-249,
# and everything is cut off flat along the bottom of the bar - the big circle
# would otherwise hang below it.
BIG = (234.0, 220.5, 43.5)      # centre x, centre y, radius
SMALL = (283.0, 212.7, 23.7)
BAR = (176.5, 207.0, 324.5, 249.0, 21.0)  # left, top, right, bottom, corner radius
# A lightning bolt drops out of the cloud's flat bottom: the storm in the
# name. It starts inside the bar, so the two read as one shape.
BOLT = ((243, 240), (271, 240), (260, 261), (278, 261), (238, 302), (251, 273), (231, 273))
BOX = (176.5, 177.0, 324.5, 302.0)
# App icons: a white cloud on the app's own dark background, so the home
# screen icon looks like the app it opens. (The favicon keeps the logo's black,
# turning white in a dark browser.)
INK = (255, 255, 255, 255)
PAPER = (14, 17, 22, 255)  # #0e1116, the app's background


def cloud_svg():
    left, top, right, bottom = BOX
    w, h = right - left, bottom - top
    bx, by, bx2, by2, r = BAR
    return f'''<svg xmlns="http://www.w3.org/2000/svg" viewBox="{left - 12} {top - 12 - (w - h) / 2} {w + 24} {w + 24}">
  <!-- The SoundStorm cloud. Black, and white where the browser is dark, so the
       tab icon never disappears into the tab. Drawn by scripts/make-icons.py. -->
  <style>path, circle, rect, polygon {{ fill: #000; }} @media (prefers-color-scheme: dark) {{ path, circle, rect, polygon {{ fill: #fff; }} }}</style>
  <clipPath id="flat"><rect x="0" y="0" width="1000" height="{by2}"/></clipPath>
  <g clip-path="url(#flat)">
    <circle cx="{BIG[0]}" cy="{BIG[1]}" r="{BIG[2]}"/>
    <circle cx="{SMALL[0]}" cy="{SMALL[1]}" r="{SMALL[2]}"/>
    <rect x="{bx}" y="{by}" width="{bx2 - bx}" height="{by2 - by}" rx="{r}"/>
  </g>
  <polygon points="{' '.join(f'{x},{y}' for x, y in BOLT)}"/>
</svg>
'''


def cloud_mark_svg():
    """The cloud alone, cropped tight: the shape the wordmark is drawn beside.
    The page uses it as a mask and paints it in the text's own colour."""
    left, top, right, bottom = BOX
    bx, by, bx2, by2, r = BAR
    return f'''<svg xmlns="http://www.w3.org/2000/svg" viewBox="{left} {top} {right - left} {bottom - top}">
  <!-- The SoundStorm cloud, cropped tight. Drawn by scripts/make-icons.py. -->
  <clipPath id="flat"><rect x="0" y="0" width="1000" height="{by2}"/></clipPath>
  <g clip-path="url(#flat)">
    <circle cx="{BIG[0]}" cy="{BIG[1]}" r="{BIG[2]}"/>
    <circle cx="{SMALL[0]}" cy="{SMALL[1]}" r="{SMALL[2]}"/>
    <rect x="{bx}" y="{by}" width="{bx2 - bx}" height="{by2 - by}" rx="{r}"/>
  </g>
  <polygon points="{' '.join(f'{x},{y}' for x, y in BOLT)}"/>
</svg>
'''


def placeholder_svg():
    """What stands in for a song or album with no cover: the cloud, soft, on a
    dark tile. Square, and scaled to fit by whatever shows it."""
    left, top, right, bottom = BOX
    w, h = right - left, bottom - top
    side = w / 0.5  # the cloud is half the tile's width
    x0 = left - (side - w) / 2
    y0 = top - (side - h) / 2
    bx, by, bx2, by2, r = BAR
    return f'''<svg xmlns="http://www.w3.org/2000/svg" viewBox="{x0:.1f} {y0:.1f} {side:.1f} {side:.1f}">
  <!-- No cover: the SoundStorm cloud on a dark tile. Drawn by scripts/make-icons.py. -->
  <defs>
    <linearGradient id="tile" x1="0" y1="0" x2="1" y2="1">
      <stop offset="0" stop-color="#252c38"/>
      <stop offset="1" stop-color="#161a21"/>
    </linearGradient>
    <clipPath id="flat"><rect x="0" y="0" width="1000" height="{by2}"/></clipPath>
  </defs>
  <rect x="{x0:.1f}" y="{y0:.1f}" width="{side:.1f}" height="{side:.1f}" fill="url(#tile)"/>
  <!-- Opacity on the group, not on each shape, or the overlaps show. -->
  <g opacity="0.28" fill="#ffffff"><g clip-path="url(#flat)">
    <circle cx="{BIG[0]}" cy="{BIG[1]}" r="{BIG[2]}"/>
    <circle cx="{SMALL[0]}" cy="{SMALL[1]}" r="{SMALL[2]}"/>
    <rect x="{bx}" y="{by}" width="{bx2 - bx}" height="{by2 - by}" rx="{r}"/>
  </g>
  <polygon points="{' '.join(f'{x},{y}' for x, y in BOLT)}"/>
  </g>
</svg>
'''


def icon(size, cloud_width):
    """A square dark icon with the white cloud centred, cloud_width of it wide."""
    scale = 4  # drawn large and scaled down, for smooth edges
    s = size * scale
    img = Image.new("RGBA", (s, s), PAPER)
    d = ImageDraw.Draw(img)
    left, top, right, bottom = BOX
    k = cloud_width * s / (right - left)
    ox = (s - (right - left) * k) / 2 - left * k
    oy = (s - (bottom - top) * k) / 2 - top * k

    def pt(x, y):
        return (ox + x * k, oy + y * k)

    for cx, cy, r in (BIG, SMALL):
        x, y = pt(cx, cy)
        d.ellipse((x - r * k, y - r * k, x + r * k, y + r * k), fill=INK)
    bx, by, bx2, by2, r = BAR
    d.rounded_rectangle((*pt(bx, by), *pt(bx2, by2)), radius=r * k, fill=INK)
    # Flat along the bottom of the bar: clear whatever hangs below it.
    d.rectangle((0, pt(0, by2)[1], s, s), fill=PAPER)
    d.polygon([pt(x, y) for x, y in BOLT], fill=INK)
    return img.resize((size, size), Image.LANCZOS).convert("RGB")


def main():
    (ASSETS / "favicon.svg").write_text(cloud_svg(), encoding="utf-8", newline="\n")
    (ASSETS / "cloud.svg").write_text(cloud_mark_svg(), encoding="utf-8", newline="\n")
    (ASSETS / "no-cover.svg").write_text(placeholder_svg(), encoding="utf-8", newline="\n")
    icons = ASSETS / "icons"
    # "any": the cloud fills most of the square. Maskable: Android may crop to
    # a circle, keeping only the middle 80%, so the cloud stays well inside it.
    icon(192, 0.68).save(icons / "icon-192.png", optimize=True)
    icon(512, 0.68).save(icons / "icon-512.png", optimize=True)
    icon(512, 0.64).save(icons / "icon-maskable-512.png", optimize=True)
    # iPhone rounds the corners itself and fills transparency with black, so
    # this one is opaque like the rest.
    icon(180, 0.66).save(icons / "apple-touch-icon.png", optimize=True)
    print("wrote favicon.svg, cloud.svg, no-cover.svg and 4 icons")


if __name__ == "__main__":
    main()
