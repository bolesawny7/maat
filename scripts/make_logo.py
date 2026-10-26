#!/usr/bin/env python3
"""Generate maat's logo assets as self-contained SVG files.

Usage: scripts/make_logo.py FONT_DIR [OUTPUT_DIR]   (output defaults to docs/assets)

The mark is the feather of Ma'at, drawn in black and white: a slender quill
whose top winds into a curl (the shaft follows a curvature profile that rises
smoothly, like an Euler spiral), with the shaft and the barb splits as negative
space. The wordmark is set in Cinzel and the social card's tagline in Cormorant
Garamond, both under the SIL Open Font License 1.1, converted to outlines so
no SVG depends on installed fonts.

Needs fontTools (`pip install fonttools`) and, in FONT_DIR, the two fonts from
https://github.com/google/fonts: ofl/cinzel/Cinzel[wght].ttf and
ofl/cormorantgaramond/CormorantGaramond[wght].ttf.

It also draws the feather in braille for the terminal banner, writing
internal/brand/feather.txt (next to the SVGs when OUTPUT_DIR is given). PNGs
are rendered from the SVGs, with rsvg-convert:

  rsvg-convert -w 512 docs/assets/maat-icon.svg -o docs/assets/maat-icon-512.png
  rsvg-convert -w 256 docs/assets/maat-icon.svg -o docs/assets/maat-icon-256.png
  rsvg-convert -w 1280 docs/assets/maat-social-preview.svg -o docs/assets/maat-social-preview.png
  rsvg-convert -w 64 docs/assets/maat-icon.svg -o internal/brand/maat-icon-64.png
"""
import io
import math
import os
import sys

from fontTools.pens.svgPathPen import SVGPathPen
from fontTools.pens.transformPen import TransformPen
from fontTools.ttLib import TTFont
from fontTools.varLib import instancer

BLACK, WHITE = "#111111", "#ffffff"


# ---------------------------------------------------------------- feather

def smoothstep(e0, e1, x):
    t = max(0.0, min(1.0, (x - e0) / (e1 - e0)))
    return t * t * (3 - 2 * t)


def bump(u, a, b):
    return 0.0 if u <= a or u >= b else math.sin(math.pi * (u - a) / (b - a)) ** 2


def spine(n=520, length=520.0, lean=-3.0, bow=0.9, curl=176.0, curl_start=0.50, curl_power=1.25):
    """Integrate the shaft from its curvature: an S-bow, then a tightening curl."""
    us = [i / (n - 1) for i in range(n)]
    ds = length / (n - 1)
    raw = [bow * 1e-3 * (-bump(u, 0.0, 0.42) + 0.8 * bump(u, 0.30, 0.66)) for u in us]
    ramp = [((u - curl_start) / (1 - curl_start)) ** curl_power if u > curl_start else 0.0 for u in us]
    kc = math.radians(curl) / (sum(ramp) * ds)
    kappa = [r + kc * c for r, c in zip(raw, ramp)]
    theta, x, y = math.radians(-90 + lean), 0.0, 0.0
    pts, heads = [(x, y)], [theta]
    for i in range(1, n):
        theta += kappa[i - 1] * ds
        x, y = x + math.cos(theta) * ds, y + math.sin(theta) * ds
        pts.append((x, y))
        heads.append(theta)
    return pts, heads, kappa, us


def widths(u, k):
    shaft = (1.0 + 4.6 * smoothstep(0.0, 0.10, u)) * (1 - 0.72 * smoothstep(0.35, 1.0, u))
    front = 47.0 * smoothstep(0.17, 0.46, u) * (1 - smoothstep(0.62, 1.0, u) ** 1.4)
    back = 13.0 * smoothstep(0.21, 0.50, u) * (1 - smoothstep(0.58, 0.96, u))
    if k > 1e-6:  # never wider than the curl's radius on the inside of the turn
        front = min(front, 0.62 / k - shaft)
    return shaft, max(front, 0.0), back


def geometry(cx, cy, height):
    pts, heads, kappa, us = spine()
    back_edge, front_edge, frames = [], [], []
    for (x, y), th, k, u in zip(pts, heads, kappa, us):
        tx, ty = math.cos(th), math.sin(th)
        nx, ny = ty, -tx  # towards the back of the feather
        sw, fw, bw = widths(u, k)
        back_edge.append((x + nx * (sw / 2 + bw), y + ny * (sw / 2 + bw)))
        front_edge.append((x - nx * (sw / 2 + fw), y - ny * (sw / 2 + fw)))
        frames.append((tx, ty, nx, ny, sw, fw, bw))
    allp = back_edge + front_edge
    minx, maxx = min(q[0] for q in allp), max(q[0] for q in allp)
    miny, maxy = min(q[1] for q in allp), max(q[1] for q in allp)
    s = height / (maxy - miny)
    ox, oy = cx - (minx + maxx) / 2 * s, cy - (miny + maxy) / 2 * s
    tf = lambda q: (q[0] * s + ox, q[1] * s + oy)  # noqa: E731
    return {"pts": pts, "us": us, "frames": frames, "tf": tf,
            "outline": [tf(q) for q in front_edge + back_edge[::-1]],
            "bbox": (minx * s + ox, miny * s + oy, maxx * s + ox, maxy * s + oy)}


def smooth_closed(points):
    pts = [points[-1]] + points + [points[0], points[1]]
    d = []
    for i in range(1, len(pts) - 2):
        p0, p1, p2, p3 = pts[i - 1], pts[i], pts[i + 1], pts[i + 2]
        c1 = (p1[0] + (p2[0] - p0[0]) / 6, p1[1] + (p2[1] - p0[1]) / 6)
        c2 = (p2[0] - (p3[0] - p1[0]) / 6, p2[1] - (p3[1] - p1[1]) / 6)
        if not d:
            d.append(f"M{p1[0]:.2f},{p1[1]:.2f}")
        d.append(f"C{c1[0]:.2f},{c1[1]:.2f} {c2[0]:.2f},{c2[1]:.2f} {p2[0]:.2f},{p2[1]:.2f}")
    return " ".join(d) + " Z"


def every(points, n):
    out = points[::n]
    return out if out[-1] == points[-1] else out + [points[-1]]


def poly(points):
    return "M" + " L".join(f"{x:.2f},{y:.2f}" for x, y in points) + " Z"


def sliver(g, u0, side, reach, angle, width, bend=0.18):
    """A thin curved split between barbs, from the vane's edge in towards the
    shaft and back towards the quill, the way barbs grow."""
    i = min(range(len(g["us"])), key=lambda k: abs(g["us"][k] - u0))
    (x, y), (tx, ty, nx, ny, sw, fw, bw) = g["pts"][i], g["frames"][i]
    sgn, vane = (-1, fw) if side == "front" else (1, bw)
    ox, oy = x + sgn * nx * (sw / 2 + vane + 3), y + sgn * ny * (sw / 2 + vane + 3)
    a = math.radians(angle)
    dx, dy = -sgn * nx * math.cos(a) - tx * math.sin(a), -sgn * ny * math.cos(a) - ty * math.sin(a)
    ln = (vane + 3) * reach / math.cos(a)
    ex, ey = ox + dx * ln, oy + dy * ln
    mx, my = (ox + ex) / 2 + tx * ln * bend, (oy + ey) / 2 + ty * ln * bend
    up, down = [], []
    for j in range(15):
        t = j / 14
        px = (1 - t) ** 2 * ox + 2 * (1 - t) * t * mx + t * t * ex
        py = (1 - t) ** 2 * oy + 2 * (1 - t) * t * my + t * t * ey
        qx, qy = 2 * (1 - t) * (mx - ox) + 2 * t * (ex - mx), 2 * (1 - t) * (my - oy) + 2 * t * (ey - my)
        qn = math.hypot(qx, qy) or 1
        h = width / 2 * (1 - t) ** 0.9
        up.append((px - qy / qn * h, py + qx / qn * h))
        down.append((px + qy / qn * h, py - qx / qn * h))
    return [g["tf"](q) for q in up + down[::-1]]


def rachis(g, start=0.18, end=0.93, w0=3.0, w1=0.5):
    """The shaft inside the vanes, as a tapering line of negative space."""
    left, right = [], []
    for (x, y), u, (tx, ty, nx, ny, sw, fw, bw) in zip(g["pts"], g["us"], g["frames"]):
        if start <= u <= end:
            h = (w0 + (w1 - w0) * (u - start) / (end - start)) / 2 * min(1.0, (u - start) / 0.02)
            left.append(g["tf"]((x + nx * h, y + ny * h)))
            right.append(g["tf"]((x - nx * h, y - ny * h)))
    return every(left, 4) + every(right, 4)[::-1]


def feather(prefix, cx, cy, height, color):
    """Defs and element for one feather. The cuts are a mask, so it works on
    any background."""
    g = geometry(cx, cy, height)
    x0, y0, x1, y1 = g["bbox"]
    cuts = [rachis(g)] + [sliver(g, *a) for a in [
        (0.32, "front", 0.78, 50, 3.4), (0.42, "front", 0.80, 50, 3.6),
        (0.52, "front", 0.72, 48, 3.2), (0.37, "back", 0.92, 52, 2.8)]]
    box = f'x="{x0 - 30:.1f}" y="{y0 - 30:.1f}" width="{x1 - x0 + 60:.1f}" height="{y1 - y0 + 60:.1f}"'
    paths = "".join(f'<path d="{poly(c)}"/>' for c in cuts)
    defs = f'\n    <mask id="{prefix}-cuts" maskUnits="userSpaceOnUse" {box}><rect {box} fill="#fff"/><g fill="#000">{paths}</g></mask>'
    body = f'<path d="{smooth_closed(every(g["outline"], 2))}" fill="{color}" mask="url(#{prefix}-cuts)"/>'
    return defs, body, g["bbox"]


# ---------------------------------------------------------------- type

class Face:
    def __init__(self, path, wght):
        font = TTFont(path)
        if "fvar" in font:
            font = instancer.instantiateVariableFont(font, {"wght": wght})
            buf = io.BytesIO()
            font.save(buf)
            buf.seek(0)
            font = TTFont(buf)
        self.font, self.upm = font, font["head"].unitsPerEm
        self.cap = font["OS/2"].sCapHeight / self.upm

    def text(self, s, size, tracking=0.0):
        """Outline path data and advance width for s, baseline at y=0."""
        cmap, gs, hmtx = self.font.getBestCmap(), self.font.getGlyphSet(), self.font["hmtx"]
        k, pen, x = size / self.upm, SVGPathPen(None), 0.0
        for i, ch in enumerate(s):
            name = cmap[ord(ch)]
            gs[name].draw(TransformPen(pen, (k, 0, 0, -k, x, 0)))
            x += hmtx[name][0] * k + (tracking * size if i < len(s) - 1 else 0)
        return pen.getCommands(), x


def place(d, x, baseline, color):
    return f'<path transform="translate({x:.2f},{baseline:.2f})" d="{d}" fill="{color}"/>'


# ---------------------------------------------------------------- terminal

BRAILLE_DOTS = {(0, 0): 0x01, (0, 1): 0x02, (0, 2): 0x04, (1, 0): 0x08,
                (1, 1): 0x10, (1, 2): 0x20, (0, 3): 0x40, (1, 3): 0x80}


def inside(poly, x, y):
    hit = False
    for (x1, y1), (x2, y2) in zip(poly, poly[1:] + poly[:1]):
        if (y1 > y) != (y2 > y) and x < (x2 - x1) * (y - y1) / (y2 - y1) + x1:
            hit = not hit
    return hit


def braille(rows=8, threshold=0.5):
    """The feather as braille text for the terminal banner. Each character is a
    2x4 grid of dots, raised where the feather covers at least half a dot."""
    h = rows * 4
    g = geometry(0, 0, h)
    x0, y0, x1, _ = g["bbox"]
    cols = math.ceil((x1 - x0) / 2) + 1
    poly = [(x - x0 + 0.5, y - y0) for x, y in every(g["outline"], 2)]
    on = {(i, j) for j in range(h) for i in range(cols * 2)
          if sum(inside(poly, i + (a + 0.5) / 3, j + (b + 0.5) / 3) for a in range(3) for b in range(3)) >= 9 * threshold}
    for (x, y), u in zip(g["pts"], g["us"]):  # keep the thin quill continuous
        if u < 0.95:
            px, py = g["tf"]((x, y))
            on.add((int(px - x0 + 0.5), min(h - 1, int(py - y0))))
    lines = []
    for r in range(rows):
        cells = [sum(bit for (dx, dy), bit in BRAILLE_DOTS.items() if (c * 2 + dx, r * 4 + dy) in on) for c in range(cols)]
        lines.append("".join(chr(0x2800 + v) if v else " " for v in cells).rstrip())
    return "\n".join(lines) + "\n"


# ---------------------------------------------------------------- assets

def document(w, h, defs, body, title):
    return (f'<svg xmlns="http://www.w3.org/2000/svg" width="{w}" height="{h}" viewBox="0 0 {w} {h}" role="img" aria-label="{title}">\n'
            f"  <title>{title}</title>\n  <defs>{defs}\n  </defs>\n  {body}\n</svg>\n")


def mark(prefix, color):
    d, b, _ = feather(prefix, 100, 256, 472, color)
    return 200, 512, d, b


def icon(prefix):
    tile = f'<rect x="16" y="16" width="480" height="480" rx="108" fill="{BLACK}"/>'
    # optically centred: the thin curl tip widens the box more than it adds weight
    d, b, _ = feather(prefix, 264, 262, 392, WHITE)
    return 512, 512, d, tile + b


def lockup(prefix, color, cinzel):
    h, fh, cap = 200, 172, 60
    d, b, (x0, _, x1, _) = feather(prefix, 0, h / 2, fh, color)
    word, ww = cinzel.text("MAAT", cap / cinzel.cap, tracking=0.22)
    left = 6 - x0  # shift so the feather starts at x=6
    tx = left + x1 + 0.62 * cap
    # the mask is in the feather's own user space, so it moves with the group
    body = f'<g transform="translate({left:.2f},0)">{b}</g>' + place(word, tx, h / 2 + cap / 2, color)
    return round(tx + ww + 6), h, d, body


def social(prefix, cinzel, cormorant):
    w, h, tx = 1280, 640, 520
    d, b, _ = feather(prefix, 300, 320, 440, WHITE)
    defs = (f'\n    <radialGradient id="{prefix}-bg" cx="0.24" cy="0.5" r="0.85">'
            f'<stop offset="0" stop-color="#242424"/><stop offset="1" stop-color="#0a0a0a"/></radialGradient>' + d)
    word, _ = cinzel.text("MAAT", 92 / cinzel.cap, tracking=0.22)
    lines = [cormorant.text(s, 46)[0] for s in ("Let AI agents troubleshoot Ubuntu,", "with permissions enforced by the kernel.")]
    labels, lw = cinzel.text("CONFINED  ·  HUMAN-APPROVED  ·  AUDITED", 17 / cinzel.cap * 0.72, tracking=0.16)
    foot, fw_ = cinzel.text("OPEN SOURCE  ·  GPL-3.0", 15 / cinzel.cap * 0.72, tracking=0.18)
    assert tx + lw < w - 60 and tx + fw_ < w - 60, "card text too wide"
    body = (f'<rect width="{w}" height="{h}" fill="url(#{prefix}-bg)"/>' + b
            + place(word, tx, 262, WHITE)
            + f'<rect x="{tx + 3}" y="300" width="64" height="1.5" fill="#8c8c8c"/>'
            + place(lines[0], tx, 370, "#ececec") + place(lines[1], tx, 424, "#ececec")
            + place(labels, tx, 500, "#a3a3a3") + place(foot, tx, 542, "#858585"))
    return w, h, defs, body


def main():
    if len(sys.argv) < 2:
        sys.exit(__doc__)
    fonts = sys.argv[1]
    root = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..")
    out = sys.argv[2] if len(sys.argv) > 2 else os.path.join(root, "docs", "assets")
    banner_dir = out if len(sys.argv) > 2 else os.path.join(root, "internal", "brand")
    os.makedirs(banner_dir, exist_ok=True)
    with open(os.path.join(banner_dir, "feather.txt"), "w", encoding="utf-8") as f:
        f.write(braille())
    print(os.path.normpath(os.path.join(banner_dir, "feather.txt")))
    cinzel = Face(os.path.join(fonts, "Cinzel[wght].ttf"), 400)
    cormorant = Face(os.path.join(fonts, "CormorantGaramond[wght].ttf"), 500)
    os.makedirs(out, exist_ok=True)
    files = {
        "maat-icon.svg": document(*icon("icon"), "maat"),
        "maat-mark.svg": document(*mark("mark", BLACK), "maat"),
        "maat-mark-white.svg": document(*mark("markw", WHITE), "maat"),
        "maat-logo-light.svg": document(*lockup("light", BLACK, cinzel), "maat"),
        "maat-logo-dark.svg": document(*lockup("dark", WHITE, cinzel), "maat"),
        "maat-social-preview.svg": document(*social("social", cinzel, cormorant), "maat: let AI agents troubleshoot Ubuntu, with permissions enforced by the kernel"),
    }
    for name, content in files.items():
        with open(os.path.join(out, name), "w") as f:
            f.write(content)
        print(os.path.normpath(os.path.join(out, name)))


if __name__ == "__main__":
    main()
