#!/usr/bin/env python3
"""Draw the pictures of Horizon's sister looks: Aurora, Dune and Nebula.

    python3 scripts/scene-art.py

Writes src/renderer/art/{aurora,dune,nebula}-deck.jpg. Like Horizon's
(scripts/horizon-art.py), each is drawn here, not photographed, so it is
Deck's own, and each look prints its own through the RGB dither
(src/renderer/backdrop/spectrum.ts): no two looks share a picture. The
output is committed: the build does not run this, and it needs Python 3
with NumPy and Pillow.

Every scene keeps its left fifth dark, where the sidebar covers it, and
its light in the middle and right, behind the composer and the cards.
"""

from pathlib import Path

import numpy as np
from PIL import Image

ROOT = Path(__file__).resolve().parent.parent
ART = ROOT / "src" / "renderer" / "art"

W, H = 2000, 1250
A = W / H
YS, XS = np.mgrid[0:H, 0:W].astype(np.float32)
X = XS / H  # 0..A
Y = YS / H  # 0..1


def value_noise(cells: int, seed: int, sx: float = 1.0, sy: float = 1.0) -> np.ndarray:
    rng = np.random.default_rng(seed)
    gw, gh = max(2, int(cells * A * sx) + 1), max(2, int(cells * sy) + 1)
    grid = rng.random((gh, gw)).astype(np.float32)
    img = Image.fromarray((grid * 255).astype(np.uint8)).resize((W, H), Image.BICUBIC)
    return np.asarray(img, dtype=np.float32) / 255


def fbm(seed: int, base: int = 4, octaves: int = 4, sx: float = 1.0, sy: float = 1.0) -> np.ndarray:
    out = np.zeros((H, W), np.float32)
    amp, total, cells = 1.0, 0.0, base
    for i in range(octaves):
        out += amp * value_noise(cells, seed + i, sx, sy)
        total += amp
        amp *= 0.55
        cells *= 2
    return out / total


def stars(seed: int, density: float, bright: float) -> np.ndarray:
    rng = np.random.default_rng(seed)
    s = np.zeros((H, W), np.float32)
    n = int(W * H * density)
    xs, ys = rng.integers(0, W, n), rng.integers(0, H, n)
    s[ys, xs] = rng.random(n).astype(np.float32) ** 3 * bright
    # A soft halo round each, so the brightest survive the dither as a dot.
    img = Image.fromarray(np.clip(s * 255, 0, 255).astype(np.uint8))
    halo = np.asarray(img.resize((W // 2, H // 2), Image.BILINEAR).resize((W, H), Image.BILINEAR), np.float32) / 255
    return np.clip(s + halo * 1.5, 0, 1)


def hold_left(rgb: np.ndarray) -> np.ndarray:
    hold = np.clip((X - 0.05 * A) / (0.17 * A), 0, 1)
    hold = hold * hold * (3 - 2 * hold)
    return rgb * (0.1 + 0.9 * hold)[..., None]


def save(rgb: np.ndarray, name: str, gain: float = 1.3) -> None:
    rgb = 1 - np.exp(-np.clip(rgb, 0, None) * gain)
    out = ART / name
    Image.fromarray((np.clip(rgb, 0, 1) ** (1 / 1.1) * 255 + 0.5).astype(np.uint8)).save(out, quality=88, optimize=True, progressive=True)
    print(f"wrote {out.relative_to(ROOT)} ({out.stat().st_size // 1024} KB)")


def col(c) -> np.ndarray:
    return np.asarray(c, np.float32)[None, None, :]


def aurora() -> None:
    """Curtains of green light with violet hems over a black ridge."""
    rgb = np.zeros((H, W, 3), np.float32)
    rgb += col((0.012, 0.02, 0.05)) * (1 - Y)[..., None] * 1.5
    rgb += stars(3, 0.0009, 1.0)[..., None] * col((0.8, 0.85, 1.0)) * (Y < 0.7)[..., None]
    # Two curtains, each a wavy hem with rays rising from it.
    for k, (y0, amp, freq, phase, seed, strength) in enumerate(
        ((0.5, 0.07, 2.1, 0.3, 11, 1.0), (0.36, 0.05, 3.0, 1.7, 23, 0.6))
    ):
        wobble = (fbm(seed, 2, 3, sy=0.2) - 0.5) * 0.16
        hem = y0 + amp * np.sin(X * freq + phase) + wobble
        above = np.clip(hem - Y, 0, None)
        below = np.clip(Y - hem, 0, None)
        rays = 0.45 + 0.75 * fbm(seed + 5, 18, 3, sx=1.0, sy=0.04)
        body = np.exp(-above / 0.17) * np.exp(-below / 0.012) * rays * strength
        # The light along the curtain: strongest a little right of centre.
        along = np.exp(-(((X / A) - 0.62) / 0.3) ** 2)
        body *= 0.25 + 0.9 * along
        green = np.exp(-above / 0.06)
        rgb += body[..., None] * (col((0.1, 1.0, 0.5)) * green[..., None] + col((0.55, 0.2, 1.0)) * (1 - green)[..., None])
        rgb += (np.exp(-np.abs(Y - hem) / 0.006) * strength * along * 0.5)[..., None] * col((0.6, 1.0, 0.8))
    # The ridge: a black silhouette, its snow catching the green.
    ridge = 0.8 + 0.06 * np.sin(X * 4.3 + 1) + 0.04 * np.sin(X * 11 + 2) + (fbm(31, 6, 3, sy=0.1) - 0.5) * 0.12
    land = Y > ridge
    glow = np.exp(-np.clip(Y - ridge, 0, None) / 0.02) * land
    rgb = np.where(land[..., None], col((0.004, 0.008, 0.01)) + glow[..., None] * col((0.06, 0.22, 0.12)), rgb)
    save(hold_left(rgb), "aurora-deck.jpg")


def dune() -> None:
    """Ridges of sand under a low sun, the sky from ember to indigo."""
    hz = 0.56
    sun_x, sun_y = 0.7 * A, 0.5
    d_sun = np.hypot(X - sun_x, Y - sun_y)
    t = np.clip(Y / hz, 0, 1)
    sky = col((0.05, 0.04, 0.16)) * (1 - t)[..., None] ** 1.2 + col((0.95, 0.38, 0.22)) * (t[..., None] ** 2.4)
    sky += col((1.0, 0.75, 0.45)) * (np.exp(-d_sun / 0.12) * 0.9)[..., None]
    sky += col((1.0, 0.95, 0.8)) * (np.exp(-((d_sun / 0.035) ** 2)) * 2.0)[..., None]
    sky += stars(5, 0.0005, 0.7)[..., None] * col((1, 0.9, 0.8)) * ((1 - t) ** 3)[..., None]
    rgb = sky
    # Four ridges, far to near: each lit on the side that faces the sun,
    # in shade on the other, and hazier the farther it lies.
    layers = ((0.57, 0.025, 2.2, 0.0, 41, 0.75), (0.64, 0.05, 1.5, 1.3, 43, 0.5), (0.75, 0.07, 1.1, 2.6, 47, 0.25), (0.9, 0.09, 0.8, 0.7, 53, 0.0))
    for base, amp, freq, phase, seed, haze in layers:
        n = fbm(seed, 2, 3, sy=0.1) - 0.5
        crest = base + amp * np.sin(X * freq * 3 + phase) + n * amp * 0.8
        # The ridge's slope, from its wave alone, so the shading is smooth:
        # a face that rises toward the sun (on the right) is lit.
        slope = amp * freq * 3 * np.cos(X * freq * 3 + phase)
        sand = Y > crest
        depth = np.clip(Y - crest, 0, None)
        lit = np.clip(0.5 - slope * 3.5, 0, 1) ** 1.5
        warm = col((0.95, 0.48, 0.2)) * lit[..., None] + col((0.1, 0.04, 0.09)) * (1 - lit)[..., None]
        warm = warm * np.exp(-depth / 0.25)[..., None] + col((0.03, 0.015, 0.03))
        rim = np.exp(-depth / 0.004) * lit * 0.9
        warm += rim[..., None] * col((1.0, 0.7, 0.4))
        layer = warm * (1 - haze) + col((0.7, 0.32, 0.25)) * haze
        rgb = np.where(sand[..., None], layer, rgb)
    save(hold_left(rgb) * 0.8, "dune-deck.jpg", gain=1.0)


def nebula() -> None:
    """A cloud of gas lit from within, magenta to blue, over deep stars."""
    cx, cy = 0.63 * A, 0.46
    r = np.hypot((X - cx) / 1.25, Y - cy)
    cloud = fbm(61, 3, 5)
    ridged = 1 - np.abs(fbm(67, 4, 4) * 2 - 1)
    density = np.clip(cloud * 1.6 - 0.45 + ridged * 0.35, 0, 1) * np.exp(-r / 0.32)
    hue = np.clip((X - cx) * 1.2 + (Y - cy) * 0.6 + 0.5 + (fbm(71, 2, 3) - 0.5), 0, 1)
    tint = col((0.95, 0.22, 0.7)) * (1 - hue)[..., None] + col((0.2, 0.45, 1.0)) * hue[..., None]
    rgb = tint * (density ** 1.4)[..., None] * 1.6
    rgb += col((1.0, 0.85, 0.95)) * (np.exp(-r / 0.05) * 0.8)[..., None]
    rgb += col((0.1, 0.9, 0.85)) * (np.clip(ridged - 0.8, 0, 1) * 2.5 * np.exp(-r / 0.25))[..., None]
    # Dust lanes that cut across the glow.
    lanes = np.clip(fbm(79, 5, 4, sy=0.4) * 2 - 1.05, 0, 1)
    rgb *= (1 - lanes * 0.85)[..., None]
    rgb += stars(7, 0.0016, 1.0)[..., None] * col((0.9, 0.9, 1.0))
    rgb += col((0.02, 0.01, 0.04))
    save(hold_left(rgb), "nebula-deck.jpg")


if __name__ == "__main__":
    aurora()
    dune()
    nebula()
