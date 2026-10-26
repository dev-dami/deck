#!/usr/bin/env python3
"""Dither the pages' pictures into 1-bit plates.

    python3 scripts/dither-art.py

Reads pictures from src/renderer/art/ (the Getty photographs there and the
Met's prints and paintings in art/met/) and writes one 1-bit PNG per plate
to src/renderer/art/plates/. Each pixel of a plate is one cell on screen
(2 CSS px or more); the page gives the two tones (ui/kit/plate.tsx), so a
plate carries no colour of its own. The output is committed: the build does
not run this, and it needs Python 3 with Pillow. src/renderer/plates.ts
lists each plate's size and credit; keep the two in step.

Atkinson's error diffusion: three quarters of each pixel's error goes to
six neighbours, an eighth to each. It keeps the highlights and shadows
clean and leaves the midtones as an even stipple, as the early Macs drew
photographs.
"""

from dataclasses import dataclass
from pathlib import Path

from PIL import Image, ImageFilter, ImageOps

ROOT = Path(__file__).resolve().parent.parent
ART = ROOT / "src" / "renderer" / "art"
OUT = ART / "plates"


@dataclass(frozen=True)
class Plate:
    src: str
    # Cells across; the height follows the crop's aspect.
    cells: int
    # The box kept, as fractions of the source: it drops a print's paper
    # margin, a mount or a dull edge.
    crop: tuple[float, float, float, float] = (0.0, 0.0, 1.0, 1.0)
    # Under 1 lifts the midtones toward the light tone.
    gamma: float = 1.0
    # The tone range: at 0.9 the lightest tone still takes one dot in ten,
    # so a blown-out sky does not go flat.
    tones: tuple[float, float] = (0.0, 1.0)
    # The channel read as tone. "L" is luminance; "R" the red, which parts a
    # woodblock print's blues (dark) from its reds, buffs and paper (light).
    channel: str = "L"


PLATES = {
    # The Welcome's wide hero: Braun's snow, sky and the pines below them.
    "alps": Plate("alpine-braun-1870.jpg", 680, (0.0, 0.0, 1.0, 0.97), gamma=0.9),
    # Pairing, a tall plate: Hiroshige's bridge in the rain, two banks joined.
    "bridge": Plate(
        "met/hiroshige-sudden-shower-over-shin-ohashi-bridge-an-36461.jpg",
        340,
        (0.075, 0.18, 0.935, 0.955),
        gamma=1.6,
        tones=(0.0, 0.97),
        channel="R",
    ),
    # Empty pages. The inbox with nothing waiting: Le Gray's still sea.
    "sea": Plate("met/le-gray-mediterranean-sea-sete-283124.jpg", 176, (0.01, 0.25, 0.99, 0.93), gamma=0.95),
    # A team with no one in it yet: Hokusai's crews rowing under the wave.
    "wave": Plate("met/hokusai-under-the-wave-off-kanagawa-also-known-a-36491.jpg", 144, (0.03, 0.035, 0.98, 0.96), tones=(0.03, 0.95)),
    # No usage to show: Hokusai's Fuji under a clear sky.
    "fuji": Plate("met/hokusai-south-wind-clear-sky-also-known-as-red-f-36490.jpg", 144, (0.03, 0.05, 0.975, 0.965), gamma=1.5, channel="R"),
}


def levels(img: Image.Image, lo: float = 0.01, hi: float = 0.99) -> Image.Image:
    """Stretch the tones so the darkest and lightest percent touch the ends."""
    hist = img.histogram()
    total = sum(hist)
    acc, black, white = 0, 0, 255
    for v, n in enumerate(hist):
        acc += n
        if acc >= total * lo:
            black = v
            break
    acc = 0
    for v in range(255, -1, -1):
        acc += hist[v]
        if acc >= total * (1 - hi):
            white = v
            break
    span = max(1, white - black)
    return img.point(lambda v: max(0, min(255, round((v - black) * 255 / span))))


def atkinson(img: Image.Image) -> Image.Image:
    w, h = img.size
    px = [float(v) for v in img.tobytes()]
    out = bytearray(w * h)
    for y in range(h):
        row = y * w
        for x in range(w):
            i = row + x
            old = px[i]
            new = 255.0 if old >= 128 else 0.0
            out[i] = 255 if new else 0
            err = (old - new) / 8
            if err == 0:
                continue
            for dx, dy in ((1, 0), (2, 0), (-1, 1), (0, 1), (1, 1), (0, 2)):
                nx, ny = x + dx, y + dy
                if 0 <= nx < w and ny < h:
                    px[ny * w + nx] += err
    return Image.frombytes("L", (w, h), bytes(out)).convert("1", dither=Image.Dither.NONE)


def render(p: Plate) -> Image.Image:
    img = ImageOps.exif_transpose(Image.open(ART / p.src)).convert("RGB")
    img = img.convert("L") if p.channel == "L" else img.getchannel(p.channel)
    w, h = img.size
    c = p.crop
    img = img.crop((round(c[0] * w), round(c[1] * h), round(c[2] * w), round(c[3] * h)))
    rows = round(img.height * p.cells / img.width)
    img = img.resize((p.cells, rows), Image.Resampling.LANCZOS)
    img = img.filter(ImageFilter.UnsharpMask(radius=1.2, percent=60, threshold=2))
    img = levels(img)
    lo, hi = p.tones
    img = img.point(lambda v: round(255 * (lo + (hi - lo) * (v / 255) ** p.gamma)))
    return atkinson(img)


def main() -> None:
    OUT.mkdir(exist_ok=True)
    for name, p in PLATES.items():
        out = render(p)
        path = OUT / f"{name}.png"
        out.save(path, optimize=True)
        print(f"{path.relative_to(ROOT)}  {out.width}x{out.height}  {path.stat().st_size // 1024} KB")


if __name__ == "__main__":
    main()
