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

from PIL import Image, ImageDraw, ImageFont

ASSETS = Path(__file__).resolve().parent.parent / "internal" / "webui" / "assets"

# The cloud in the logo's own pixels. Its box is x 176.5-324.5, y 177-249,
# and everything is cut off flat along the bottom of the bar - the big circle
# would otherwise hang below it.
BIG = (234.0, 220.5, 43.5)      # center x, center y, radius
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
    The page uses it as a mask and paints it in the text's own color."""
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
    """A square dark icon with the white cloud centered, cloud_width of it wide."""
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


def banner(width, height):
    """The TV home screen's banner: the cloud, then the name in heavy italic as
    the wordmark has it, on the app's dark background. Android TV shows a
    banner, not the square icon, in its row of apps."""
    scale = 4
    w, h = width * scale, height * scale
    img = Image.new("RGBA", (w, h), PAPER)
    d = ImageDraw.Draw(img)
    font = None
    # Segoe UI Bold Italic where it exists (Windows), else any bold italic.
    for name in ("segoeuiz.ttf", "C:/Windows/Fonts/segoeuiz.ttf", "DejaVuSans-BoldOblique.ttf"):
        try:
            font = ImageFont.truetype(name, int(h * 0.19))
            break
        except OSError:
            continue
    if font is None:
        return None  # main() leaves the banner as it is
    text = "SoundStorm"
    tb = d.textbbox((0, 0), text, font=font)
    tw, th = tb[2] - tb[0], tb[3] - tb[1]
    left, top, right, bottom = BOX
    k = h * 0.40 / (bottom - top)  # the cloud and bolt 40% of the height
    cw = (right - left) * k
    gap = h * 0.08
    x0 = (w - (cw + gap + tw)) / 2
    ox = x0 - left * k
    oy = (h - (bottom - top) * k) / 2 - top * k

    def pt(x, y):
        return (ox + x * k, oy + y * k)

    for cx, cy, r in (BIG, SMALL):
        x, y = pt(cx, cy)
        d.ellipse((x - r * k, y - r * k, x + r * k, y + r * k), fill=INK)
    bx, by, bx2, by2, r = BAR
    d.rounded_rectangle((*pt(bx, by), *pt(bx2, by2)), radius=r * k, fill=INK)
    d.rectangle((pt(bx, by2)[0] - 4, pt(0, by2)[1], pt(bx2, by2)[0] + 4, pt(0, bottom)[1] + 4), fill=PAPER)
    d.polygon([pt(x, y) for x, y in BOLT], fill=INK)
    d.text((x0 + cw + gap - tb[0], (h - th) / 2 - tb[1]), text, font=font, fill=INK)
    return img.resize((width, height), Image.LANCZOS).convert("RGB")


def cloud_mask(w, h, cloud_height):
    """The cloud as a mask, centered on a w x h canvas, cloud_height of it tall.
    A mask rather than drawn in colour, because the flat bottom is cut by
    clearing what hangs below the bar, and an Apple TV icon's front layer has
    no background to clear it with."""
    scale = 4
    W, H = w * scale, h * scale
    m = Image.new("L", (W, H), 0)
    d = ImageDraw.Draw(m)
    left, top, right, bottom = BOX
    k = cloud_height * H / (bottom - top)
    ox = (W - (right - left) * k) / 2 - left * k
    oy = (H - (bottom - top) * k) / 2 - top * k

    def pt(x, y):
        return (ox + x * k, oy + y * k)

    for cx, cy, r in (BIG, SMALL):
        x, y = pt(cx, cy)
        d.ellipse((x - r * k, y - r * k, x + r * k, y + r * k), fill=255)
    bx, by, bx2, by2, r = BAR
    d.rounded_rectangle((*pt(bx, by), *pt(bx2, by2)), radius=r * k, fill=255)
    d.rectangle((0, pt(0, by2)[1], W, H), fill=0)
    d.polygon([pt(x, y) for x, y in BOLT], fill=255)
    return m.resize((w, h), Image.LANCZOS)


def tv_layer(w, h, front):
    """An Apple TV icon layer: the dark back, or the white cloud on nothing.
    tvOS lifts the front over the back as the icon takes the focus."""
    if not front:
        return Image.new("RGB", (w, h), PAPER[:3])
    img = Image.new("RGBA", (w, h), (0, 0, 0, 0))
    img.paste(Image.new("RGBA", (w, h), INK), (0, 0), cloud_mask(w, h, 0.5))
    return img


def tv_shelf(w, h):
    """The Apple TV's Top Shelf picture: the cloud on the dark, flat."""
    img = Image.new("RGB", (w, h), PAPER[:3])
    img.paste(Image.new("RGB", (w, h), INK[:3]), (0, 0), cloud_mask(w, h, 0.4))
    return img


def write_tv_icons(xcassets):
    """The Apple TV app's brand assets: a two-layer icon at 400x240 (and @2x)
    for the home screen, 1280x768 for the App Store, and the Top Shelf, with
    the Contents.json files Xcode needs to find them."""
    import json
    brand = xcassets / "App Icon & Top Shelf Image.brandassets"
    info = {"info": {"author": "xcode", "version": 1}}

    def write(path, obj):
        path.mkdir(parents=True, exist_ok=True)
        (path / "Contents.json").write_text(json.dumps(obj, indent=2) + "\n", encoding="utf-8", newline="\n")

    stacks = [("App Icon.imagestack", [(400, 240, "1x"), (800, 480, "2x")]),
              ("App Icon - App Store.imagestack", [(1280, 768, "1x")])]
    for name, sizes in stacks:
        write(brand / name, {"layers": [{"filename": "Front.imagestacklayer"},
                                        {"filename": "Back.imagestacklayer"}], **info})
        for layer, front in (("Front", True), ("Back", False)):
            content = brand / name / f"{layer}.imagestacklayer" / "Content.imageset"
            write(content.parent, info)
            content.mkdir(parents=True, exist_ok=True)
            images = []
            for w, h, sc in sizes:
                fname = f"{layer.lower()}-{w}x{h}.png"
                tv_layer(w, h, front).save(content / fname, optimize=True)
                images.append({"filename": fname, "idiom": "tv", "scale": sc})
            write(content, {"images": images, **info})
    for name, sizes in (("Top Shelf Image.imageset", [(1920, 720, "1x"), (3840, 1440, "2x")]),
                        ("Top Shelf Image Wide.imageset", [(2320, 720, "1x"), (4640, 1440, "2x")])):
        (brand / name).mkdir(parents=True, exist_ok=True)
        images = []
        for w, h, sc in sizes:
            fname = f"shelf-{w}x{h}.png"
            tv_shelf(w, h).save(brand / name / fname, optimize=True)
            images.append({"filename": fname, "idiom": "tv", "scale": sc})
        write(brand / name, {"images": images, **info})
    write(brand, {"assets": [
        {"filename": "App Icon - App Store.imagestack", "idiom": "tv", "role": "primary-app-icon", "size": "1280x768"},
        {"filename": "App Icon.imagestack", "idiom": "tv", "role": "primary-app-icon", "size": "400x240"},
        {"filename": "Top Shelf Image Wide.imageset", "idiom": "tv", "role": "top-shelf-image-wide", "size": "2320x720"},
        {"filename": "Top Shelf Image.imageset", "idiom": "tv", "role": "top-shelf-image", "size": "1920x720"},
    ], **info})
    # The connect and sign-in screens' logo: the white cloud on nothing,
    # over the app's own dark.
    logo = xcassets / "Logo.imageset"
    logo.mkdir(parents=True, exist_ok=True)
    images = []
    for side, sc in ((200, "1x"), (400, "2x")):
        tv_layer(side, side, True).save(logo / f"logo-{side}.png", optimize=True)
        images.append({"filename": f"logo-{side}.png", "idiom": "tv", "scale": sc})
    write(logo, {"images": images, **info})
    write(xcassets, info)


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
    # The iPhone app's icon: one 1024px image, which Xcode scales to every
    # size. Same look as the home screen icon above, and opaque for the same
    # reason - the App Store refuses an icon with transparency.
    # The connect screen shows the same icon, and an app cannot load its own
    # icon set as an image, so it gets a smaller copy of its own.
    xcassets = ASSETS.parents[2] / "ios" / "SoundStorm" / "Assets.xcassets"
    icon(1024, 0.66).save(xcassets / "AppIcon.appiconset" / "icon-1024.png", optimize=True)
    icon(240, 0.66).save(xcassets / "Logo.imageset" / "logo.png", optimize=True)
    # The Android app's launcher icon is adaptive: a 108dp square of which a
    # launcher may show as little as the middle 66dp circle, so the cloud is
    # sized to that circle (0.66 of 66/108). Opaque, the background colour
    # being the same as the adaptive icon's own. Its connect screen shows a
    # copy like the iPhone app's.
    res = ASSETS.parents[2] / "android" / "app" / "src" / "main" / "res" / "drawable-nodpi"
    res.mkdir(parents=True, exist_ok=True)
    icon(432, 0.40).save(res / "ic_launcher_foreground.png", optimize=True)
    icon(240, 0.66).save(res / "logo.png", optimize=True)
    # The TV banner: 320x180dp, drawn for xhdpi, the density of a 1080p TV.
    tv = ASSETS.parents[2] / "android" / "app" / "src" / "main" / "res" / "drawable-xhdpi"
    tv.mkdir(parents=True, exist_ok=True)
    # Only where its font is (Windows): drawn in another, it would change every
    # time the script ran on the other machine.
    android_banner = banner(640, 360)
    if android_banner is not None:
        android_banner.save(tv / "tv_banner.png", optimize=True)
    else:
        print("no Segoe UI Bold Italic here: left the Android TV banner as it is")
    write_tv_icons(ASSETS.parents[2] / "ios" / "SoundStormTV" / "Assets.xcassets")
    print("wrote favicon.svg, cloud.svg, no-cover.svg, 8 icons, the TV banner and the Apple TV icons")


if __name__ == "__main__":
    main()
