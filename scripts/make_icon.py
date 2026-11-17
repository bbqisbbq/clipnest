"""Generate ClipNest's app icon (PNG + multi-size ICO).

Design: rounded-square gradient plate (indigo -> violet) with a white clipboard
glyph. Kept deterministic so re-running produces the same bytes.
"""

from pathlib import Path

from PIL import Image, ImageDraw

ROOT = Path(__file__).resolve().parents[1]
BUILD = ROOT / "build"

S = 1024


def lerp(a, b, t):
    return tuple(round(x + (y - x) * t) for x, y in zip(a, b))


def gradient(w, h, top=(0x5B, 0x7C, 0xFF), bottom=(0x8A, 0x4C, 0xF0)):
    img = Image.new("RGB", (w, h))
    px = img.load()
    for y in range(h):
        row = lerp(top, bottom, y / max(1, h - 1))
        for x in range(w):
            px[x, y] = row
    return img


def rounded_mask(size, radius, scale=4):
    mask = Image.new("L", (size * scale, size * scale), 0)
    d = ImageDraw.Draw(mask)
    d.rounded_rectangle((0, 0, size * scale - 1, size * scale - 1), radius=radius * scale, fill=255)
    return mask.resize((size, size), Image.LANCZOS)


def build_icon():
    plate = gradient(S, S).convert("RGBA")
    plate.putalpha(rounded_mask(S, radius=210))

    glyph = Image.new("RGBA", (S, S), (0, 0, 0, 0))
    d = ImageDraw.Draw(glyph)

    # Clipboard body: rounded rectangle with a sheet peeking out of the top.
    body = (250, 210, 774, 838)
    d.rounded_rectangle(body, radius=74, fill=(255, 255, 255, 235))

    # Inner sheet lines (punched out later via composite).
    lines = Image.new("RGBA", (S, S), (0, 0, 0, 0))
    ld = ImageDraw.Draw(lines)
    for i, y in enumerate((400, 490, 580, 670)):
        width = 280 if i < 3 else 190
        ld.rounded_rectangle((330, y, 330 + width, y + 34), radius=17, fill=(255, 255, 255, 255))

    # Card that holds the clip.
    d.rounded_rectangle((372, 150, 652, 268), radius=54, fill=(255, 255, 255, 255))
    d.rounded_rectangle((430, 186, 594, 232), radius=23, fill=(0x6B, 0x6B, 0xF5, 255))

    # Compose: plate + body, then carve the lines out of the body.
    solid = Image.alpha_composite(plate, glyph)
    carve = Image.new("RGBA", (S, S), (0, 0, 0, 0))
    carve.paste((0, 0, 0, 0), (0, 0, S, S), lines)
    body_area = Image.new("RGBA", (S, S), (0, 0, 0, 0))
    body_area.paste(solid, (0, 0, S, S), Image.new("L", (S, S), 0))

    # Draw the lines in plate colour instead of erasing (keeps edges clean).
    line_color = (0x6E, 0x76, 0xF3, 255)
    ld2 = ImageDraw.Draw(solid)
    for i, y in enumerate((400, 490, 580, 670)):
        width = 280 if i < 3 else 190
        ld2.rounded_rectangle((330, y, 330 + width, y + 30), radius=15, fill=line_color)

    return solid


def main():
    icon = build_icon()
    BUILD.mkdir(parents=True, exist_ok=True)
    icon.save(BUILD / "appicon.png")
    icon.resize((256, 256), Image.LANCZOS).save(BUILD / "appicon-256.png")

    win = BUILD / "windows"
    win.mkdir(parents=True, exist_ok=True)
    icon.save(
        win / "icon.ico",
        sizes=[(16, 16), (24, 24), (32, 32), (48, 48), (64, 64), (128, 128), (256, 256)],
    )
    print("wrote", BUILD / "appicon.png", BUILD / "windows" / "icon.ico")


if __name__ == "__main__":
    main()
