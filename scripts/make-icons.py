#!/usr/bin/env python3
"""Draw SoundStorm's app icons from the cloud in the logo.

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
BOX = (176.5, 177.0, 324.5, 249.0)
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
  <style>path, circle, rect {{ fill: #000; }} @media (prefers-color-scheme: dark) {{ path, circle, rect {{ fill: #fff; }} }}</style>
  <clipPath id="flat"><rect x="0" y="0" width="1000" height="{by2}"/></clipPath>
  <g clip-path="url(#flat)">
    <circle cx="{BIG[0]}" cy="{BIG[1]}" r="{BIG[2]}"/>
    <circle cx="{SMALL[0]}" cy="{SMALL[1]}" r="{SMALL[2]}"/>
    <rect x="{bx}" y="{by}" width="{bx2 - bx}" height="{by2 - by}" rx="{r}"/>
  </g>
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
    return img.resize((size, size), Image.LANCZOS).convert("RGB")


def main():
    (ASSETS / "favicon.svg").write_text(cloud_svg(), encoding="utf-8", newline="\n")
    (ASSETS / "cloud.svg").write_text(cloud_mark_svg(), encoding="utf-8", newline="\n")
    icons = ASSETS / "icons"
    # "any": the cloud fills most of the square. Maskable: Android may crop to
    # a circle, keeping only the middle 80%, so the cloud stays well inside it.
    icon(192, 0.68).save(icons / "icon-192.png", optimize=True)
    icon(512, 0.68).save(icons / "icon-512.png", optimize=True)
    icon(512, 0.64).save(icons / "icon-maskable-512.png", optimize=True)
    # iPhone rounds the corners itself and fills transparency with black, so
    # this one is opaque like the rest.
    icon(180, 0.66).save(icons / "apple-touch-icon.png", optimize=True)
    print("wrote favicon.svg, cloud.svg and 4 icons")


if __name__ == "__main__":
    main()
