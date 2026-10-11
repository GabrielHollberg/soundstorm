"""Draws the disk image's window: where EmberStorm and Applications sit, an
arrow between them, and what to do. Run with Pillow (mac/make-dmg.sh does):
writes background.png (600x350) and background@2x.png beside this file.

The middle is a mid tone on purpose: Finder writes the icons' names itself,
black in Light Mode and white in Dark Mode, and both must read on it."""
from pathlib import Path
from PIL import Image, ImageDraw, ImageFont

HERE = Path(__file__).parent
W, H = 600, 350
APP, APPS = (160, 190), (440, 190)  # icon centres, kept in step with settings.py
FONT = "/System/Library/Fonts/SFNS.ttf"


def draw(scale):
    w, h = W * scale, H * scale
    img = Image.new("RGB", (w, h))
    px = img.load()
    top, bottom = (126, 136, 154), (104, 114, 132)
    for y in range(h):
        t = y / (h - 1)
        c = tuple(round(a + (b - a) * t) for a, b in zip(top, bottom))
        for x in range(w):
            px[x, y] = c
    d = ImageDraw.Draw(img)

    def text(s, y, size, fill, weight=None):
        f = ImageFont.truetype(FONT, size * scale)
        if weight:
            try:
                f.set_variation_by_name(weight)
            except Exception:
                pass
        x = (w - d.textlength(s, font=f)) / 2
        d.text((x, y * scale), s, font=f, fill=fill)

    text("Drag EmberStorm into Applications", 34, 22, (255, 255, 255), "Semibold")
    text("Then open it from Applications to set it up.", 288, 14, (236, 240, 247))

    # The arrow, from beside one icon to beside the other.
    y = APP[1] * scale
    x0, x1 = (APP[0] + 86) * scale, (APPS[0] - 86) * scale
    head = 16 * scale
    d.line([(x0, y), (x1 - head, y)], fill=(255, 255, 255), width=6 * scale)
    d.polygon([(x1, y), (x1 - head - 4 * scale, y - head), (x1 - head - 4 * scale, y + head)], fill=(255, 255, 255))
    return img


draw(1).save(HERE / "background.png")
draw(2).save(HERE / "background@2x.png")
