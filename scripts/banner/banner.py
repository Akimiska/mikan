"""Draws the README banners: .github/assets/banner-{en,ru}-{light,dark}.svg.

The wordmark and the captions are real glyph outlines of the panel's fonts (Unbounded,
Onest), shaped by HarfBuzz, so GitHub shows them the same everywhere without fonts.
The light is the panel's "dawn"; it drifts slowly, the mandarin floats.

    docker run --rm -v "$PWD:/src" -w /src python:3.13-slim sh -c \
      "pip install -q fonttools brotli uharfbuzz && python scripts/banner/banner.py"

Needs the fonts from web/node_modules (pnpm install in web/ first).
"""

import glob
import io
import os

import uharfbuzz as hb
from fontTools.pens.svgPathPen import SVGPathPen
from fontTools.pens.transformPen import TransformPen
from fontTools.ttLib import TTFont
from fontTools.varLib.instancer import instantiateVariableFont

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
OUT = os.path.join(ROOT, ".github", "assets")
W, H = 1280, 360


def font_file(package, name):
    """A fontsource file: pnpm links web/node_modules/@fontsource-variable/<package> into
    its store, and the link does not survive a Docker bind mount on Windows."""
    for pattern in (
        os.path.join(ROOT, "web", "node_modules", "@fontsource-variable", package, "files", name),
        os.path.join(ROOT, "web", "node_modules", ".pnpm", f"@fontsource-variable+{package}@*", "node_modules", "@fontsource-variable", package, "files", name),
    ):
        found = sorted(glob.glob(pattern))
        if found:
            return found[-1]
    raise FileNotFoundError(f"{package}/{name}: run pnpm install in web/")


class Face:
    """A variable font pinned to one weight: HarfBuzz shapes, fontTools draws."""

    def __init__(self, files, weight):
        self.fonts = []
        for package, name in files:
            src = TTFont(font_file(package, name))
            src.flavor = None
            static = instantiateVariableFont(src, {"wght": weight}, inplace=False)
            buf = io.BytesIO()
            static.save(buf)
            face = hb.Face(buf.getvalue())
            font = hb.Font(face)
            self.fonts.append((static, font, static.getBestCmap(), static.getGlyphOrder(), face.upem))

    def segments(self, text):
        """Splits text into runs each file covers (fontsource splits Latin and Cyrillic);
        a space stays with the run it is in."""
        runs = []
        for ch in text:
            covering = next((f for f in self.fonts if ord(ch) in f[2]), None)
            if covering is None:
                raise ValueError(f"no font covers {ch!r}")
            if runs and (ch == " " or runs[-1][0] is covering):
                runs[-1][1].append(ch)
            else:
                runs.append((covering, [ch]))
        return [(f, "".join(chars)) for f, chars in runs]

    def run(self, text, size, tracking=0.0):
        """Shapes text: (paths as one SVG path string at origin, advance width)."""
        x, parts = 0.0, []
        for (static, font, _, order, upem), chunk in self.segments(text):
            buf = hb.Buffer()
            buf.add_str(chunk)
            buf.guess_segment_properties()
            hb.shape(font, buf, {"kern": True, "liga": True})
            glyphs = static.getGlyphSet()
            k = size / upem
            for info, pos in zip(buf.glyph_infos, buf.glyph_positions):
                pen = SVGPathPen(glyphs)
                glyphs[order[info.codepoint]].draw(TransformPen(pen, (k, 0, 0, -k, x + pos.x_offset * k, -pos.y_offset * k)))
                cmd = pen.getCommands()
                if cmd:
                    parts.append(cmd)
                x += pos.x_advance * k + tracking * size
        return " ".join(parts), x - tracking * size


def num(v):
    return f"{v:.1f}".rstrip("0").rstrip(".")


THEMES = {
    "light": {
        "base": "#fbf1e8",
        "edge": "rgba(255,255,255,.85)",
        "glows": [
            ("#ff8a48", 0.78, 170, 30, 330),
            ("#ff7696", 0.42, 640, -40, 300),
            ("#9888ff", 0.40, 1060, 110, 330),
            ("#5cb4e8", 0.48, 1170, 390, 310),
            ("#ffc260", 0.52, 380, 430, 300),
        ],
        "ink": "#161a24",
        "sub": "#5c6474",
        "pill": "rgba(255,255,255,.66)",
        "pillEdge": "rgba(255,255,255,.95)",
        "pillInk": "#252b38",
        "shadow": "rgba(140,60,10,.20)",
    },
    "dark": {
        "base": "#131119",
        "edge": "rgba(255,255,255,.10)",
        "glows": [
            ("#ff7a3a", 0.55, 170, 30, 330),
            ("#ff5c86", 0.30, 640, -40, 300),
            ("#7f6cff", 0.34, 1060, 110, 330),
            ("#3f9fe0", 0.30, 1170, 390, 310),
            ("#ffae3c", 0.26, 380, 430, 300),
        ],
        "ink": "#f7efe6",
        "sub": "#b7b0c4",
        "pill": "rgba(255,255,255,.07)",
        "pillEdge": "rgba(255,255,255,.14)",
        "pillInk": "#ece5f3",
        "shadow": "rgba(0,0,0,.35)",
    },
}

COPY = {
    "en": {"tagline": "VPN panel on the mihomo core", "pills": ["15 protocols", "Telegram bot & Mini App", "Auto-updates"]},
    "ru": {"tagline": "VPN-панель на ядре mihomo", "pills": ["15 протоколов", "Бот и Mini App", "Автообновления"]},
}


def banner(lang, theme, display, ui, ui_bold):
    t, c = THEMES[theme], COPY[lang]
    word, word_w = display.run("mikan", 150, tracking=-0.05)
    tag, _ = ui.run(c["tagline"], 36)
    x0 = 380
    pills, px = [], x0
    for label in c["pills"]:
        path, w = ui_bold.run(label, 22)
        pw = w + 34 + 26
        pills.append((px, pw, path))
        px += pw + 12
    glows = "".join(
        f'<radialGradient id="g{i}"><stop offset="0" stop-color="{col}" stop-opacity="{op}"/><stop offset="1" stop-color="{col}" stop-opacity="0"/></radialGradient>'
        for i, (col, op, *_rest) in enumerate(t["glows"])
    )
    circles = "".join(
        f'<circle class="d{i % 3}" cx="{cx}" cy="{cy}" r="{r}" fill="url(#g{i})"/>'
        for i, (_c, _o, cx, cy, r) in enumerate(t["glows"])
    )
    pill_svg = "".join(
        f'<g transform="translate({num(x)} 286)"><rect width="{num(w)}" height="40" rx="20" fill="{t["pill"]}" stroke="{t["pillEdge"]}"/>'
        f'<circle cx="20" cy="20" r="5" fill="#f07a2e"/><path transform="translate(34 28)" d="{p}" fill="{t["pillInk"]}"/></g>'
        for x, w, p in pills
    )
    return f"""<svg xmlns="http://www.w3.org/2000/svg" width="{W}" height="{H}" viewBox="0 0 {W} {H}" role="img" aria-label="mikan — {c['tagline']}">
<title>mikan — {c['tagline']}</title>
<defs>
{glows}
<radialGradient id="fruit" cx=".36" cy=".3" r=".85"><stop offset="0" stop-color="#ffc58f"/><stop offset=".45" stop-color="#f7883a"/><stop offset="1" stop-color="#d45a16"/></radialGradient>
<radialGradient id="hi" cx=".5" cy=".5" r=".5"><stop offset="0" stop-color="#fff" stop-opacity=".8"/><stop offset="1" stop-color="#fff" stop-opacity="0"/></radialGradient>
<clipPath id="card"><rect width="{W}" height="{H}" rx="32"/></clipPath>
</defs>
<style>
.d0{{animation:a 17s ease-in-out infinite alternate}}
.d1{{animation:b 21s ease-in-out infinite alternate}}
.d2{{animation:a 24s ease-in-out infinite alternate-reverse}}
.float{{animation:f 6s ease-in-out infinite}}
.shade{{transform-box:fill-box;transform-origin:50% 50%;animation:s 6s ease-in-out infinite}}
.leaf{{transform-box:fill-box;transform-origin:0% 100%;animation:l 5s ease-in-out infinite}}
@keyframes a{{to{{transform:translate(60px,26px)}}}}
@keyframes b{{to{{transform:translate(-54px,30px)}}}}
@keyframes f{{50%{{transform:translateY(-9px)}}}}
@keyframes s{{50%{{transform:scale(.88);opacity:.75}}}}
@keyframes l{{50%{{transform:rotate(-7deg)}}}}
@media (prefers-reduced-motion:reduce){{.d0,.d1,.d2,.float,.shade,.leaf{{animation:none}}}}
</style>
<g clip-path="url(#card)">
<rect width="{W}" height="{H}" fill="{t['base']}"/>
{circles}
</g>
<rect x=".5" y=".5" width="{W - 1}" height="{H - 1}" rx="31.5" fill="none" stroke="{t['edge']}"/>
<ellipse class="shade" cx="236" cy="296" rx="80" ry="11" fill="{t['shadow']}"/>
<g class="float"><g transform="translate(116 44) scale(7.5)">
<circle cx="16" cy="18" r="12" fill="url(#fruit)"/>
<ellipse cx="12" cy="13.4" rx="4.2" ry="2.7" fill="url(#hi)" transform="rotate(-28 12 13.4)"/>
<path d="M16 6.2v3" stroke="#1f7650" stroke-width="1.6" stroke-linecap="round"/>
<path class="leaf" d="M16 7.5c.3-2.6 2.4-4.4 5.6-4.4-.2 2.9-2.4 4.7-5.6 4.4Z" fill="#2f9e6b"/>
</g></g>
<path transform="translate({x0 - 6} 196)" d="{word}" fill="{t['ink']}"/>
<path transform="translate({x0} 252)" d="{tag}" fill="{t['sub']}"/>
{pill_svg}
</svg>
"""


def main():
    onest = [("onest", "onest-latin-wght-normal.woff2"), ("onest", "onest-cyrillic-wght-normal.woff2")]
    display = Face([("unbounded", "unbounded-latin-wght-normal.woff2")], 800)
    ui = Face(onest, 600)
    ui_bold = Face(onest, 700)
    os.makedirs(OUT, exist_ok=True)
    for lang in COPY:
        for theme in THEMES:
            path = os.path.join(OUT, f"banner-{lang}-{theme}.svg")
            with open(path, "w", encoding="utf-8", newline="\n") as f:
                f.write(banner(lang, theme, display, ui, ui_bold))
            print(path, os.path.getsize(path), "bytes")


if __name__ == "__main__":
    main()
