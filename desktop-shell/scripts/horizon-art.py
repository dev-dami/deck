#!/usr/bin/env python3
"""Draw Horizon's picture: a planet's lit limb seen from orbit.

    python3 scripts/horizon-art.py

Writes src/renderer/art/horizon-deck.jpg, the picture the Horizon look
prints through its RGB dither (src/renderer/backdrop/spectrum.ts). It is
drawn here, not photographed, so it is Deck's own and needs no credit. The
output is committed: the build does not run this, and it needs Python 3
with NumPy and Pillow.

The limb is an arc of a large circle whose centre lies far below and to the
left of the frame, so it runs from high on the left, past the sidebar,
down to the bottom of the right edge. Above it, space, with the atmosphere's
blue scattered along it; on it, a thin white-gold line of sunlight; below
it, the night side, lit orange and then red where the sun grazes it. The
left third stays dark: the sidebar covers it in the window.
"""

from pathlib import Path

import numpy as np
from PIL import Image

ROOT = Path(__file__).resolve().parent.parent
OUT = ROOT / "src" / "renderer" / "art" / "horizon-deck.jpg"

W, H = 2000, 1250


def value_noise(w: int, h: int, cells: int, seed: int) -> np.ndarray:
    """Smooth noise in 0..1 from a coarse random grid, bicubic-upsampled."""
    rng = np.random.default_rng(seed)
    grid = rng.random((cells, int(cells * w / h) + 1)).astype(np.float32)
    img = Image.fromarray((grid * 255).astype(np.uint8)).resize((w, h), Image.BICUBIC)
    return np.asarray(img, dtype=np.float32) / 255


def fbm(w: int, h: int, seed: int) -> np.ndarray:
    out = np.zeros((h, w), np.float32)
    amp, total = 1.0, 0.0
    for i, cells in enumerate((4, 9, 20, 44)):
        out += amp * value_noise(w, h, cells, seed + i)
        total += amp
        amp *= 0.55
    return out / total


def main() -> None:
    a = W / H
    ys, xs = np.mgrid[0:H, 0:W].astype(np.float32)
    X = xs / H  # 0..a
    Y = ys / H  # 0..1

    # The limb: a circle through P0 (high on the left, past the sidebar)
    # and P1 (low on the right edge), its centre at height cy.
    p0, p1, cy = (0.18 * a, 0.2), (a, 1.04), 3.2
    cx = ((p1[0] ** 2 + (p1[1] - cy) ** 2) - (p0[0] ** 2 + (p0[1] - cy) ** 2)) / (2 * (p1[0] - p0[0]))
    r = float(np.hypot(p0[0] - cx, p0[1] - cy))
    dist = np.hypot(X - cx, Y - cy)
    d = dist - r  # > 0 above the limb (space), < 0 below (the night side)
    # Where along the limb, 0 where it enters at the top to 1 at the right.
    ang = np.arctan2(Y - cy, X - cx)
    a0 = np.arctan2(p0[1] - cy, p0[0] - cx)
    a1 = np.arctan2(p1[1] - cy, p1[0] - cx)
    t = np.clip((ang - a0) / (a1 - a0), -0.2, 1.3)

    clouds = fbm(W, H, 7)
    streaks = fbm(W, H, 21)

    rgb = np.zeros((H, W, 3), np.float32)

    def add(colour, weight):
        rgb[...] += np.asarray(colour, np.float32)[None, None, :] * weight[..., None]

    above = np.clip(d, 0, None)
    below = np.clip(-d, 0, None)
    up = d > 0
    # The sun sits on the limb a fifth of the way along; light falls off
    # either side of it.
    sun = np.exp(-(((t - 0.2) / 0.22) ** 2))
    along = np.clip(1.15 - 0.75 * t, 0.2, 1.2)

    # Space: a faint blue that deepens toward the limb.
    add((0.015, 0.03, 0.09), np.exp(-above / 0.45) * up * along)
    # The atmosphere: a wide blue haze, broken by high cloud, and a bright
    # cyan rim on the limb itself.
    haze = np.exp(-above / 0.09) * up * (0.55 + 0.75 * clouds) * (0.45 + 0.8 * sun) * along
    add((0.12, 0.38, 1.0), haze)
    add((0.55, 0.82, 1.0), np.exp(-above / 0.018) * up * (0.5 + 0.9 * sun))
    # The line of sunlight on the limb.
    core = np.exp(-((d / 0.0065) ** 2)) * (0.55 + 0.9 * sun)
    add((1.0, 0.93, 0.72), core)
    # The night side, lit along the edge: gold, then orange, then a long
    # red glow that strengthens toward the right, streaked by weather.
    lit = ~up
    add((1.0, 0.62, 0.16), np.exp(-below / 0.016) * lit * (0.5 + 0.8 * sun))
    add((1.0, 0.32, 0.06), np.exp(-below / 0.03) * lit * (0.35 + 0.5 * t) * (0.7 + 0.5 * streaks))
    add((0.75, 0.08, 0.04), np.exp(-below / 0.06) * lit * (0.18 + 0.5 * np.clip(t, 0, 1)) * (0.6 + 0.6 * streaks))
    add((0.07, 0.012, 0.025), np.exp(-below / 0.22) * lit)
    # A bloom round the sun.
    sx, sy = cx + r * np.cos(a0 + 0.2 * (a1 - a0)), cy + r * np.sin(a0 + 0.2 * (a1 - a0))
    bloom = (X - sx) ** 2 + (Y - sy) ** 2
    add((1.0, 0.85, 0.7), 0.5 * np.exp(-bloom / 0.004) * (0.35 + 0.65 * up))
    add((0.3, 0.5, 1.0), 0.3 * np.exp(-bloom / 0.05) * up)

    # The sidebar's third, held back to near black.
    hold = np.clip((X - 0.06 * a) / (0.16 * a), 0, 1)
    hold = hold * hold * (3 - 2 * hold)
    rgb *= (0.12 + 0.88 * hold)[..., None]

    # Tone map, so the core whitens instead of clipping a hue.
    rgb = 1 - np.exp(-rgb * 1.35)
    img = Image.fromarray((np.clip(rgb, 0, 1) ** (1 / 1.1) * 255 + 0.5).astype(np.uint8))
    img.save(OUT, quality=88, optimize=True, progressive=True)
    print(f"wrote {OUT.relative_to(ROOT)} ({OUT.stat().st_size // 1024} KB)")


if __name__ == "__main__":
    main()
