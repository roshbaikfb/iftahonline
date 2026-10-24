#!/usr/bin/env python3
"""Builds the storefront's Iftah Sans Arabic from IBM Plex Sans Arabic.

The Fontsource Arabic subset of IBM Plex Sans Arabic is three times the
size of Cairo's, which pushes the storefront's LCP past its budget on a
slow phone network. This keeps what Egyptian and Gulf stores write:

- the Arabic block (U+0600-06FF), the joiners and marks (U+200C-200F),
  dashes and punctuation (U+2010-2027), ASCII digits and symbols, and the
  no-break space;
- every OpenType layout feature, so shaping is unchanged.

It drops the Arabic Supplement and the presentation forms, which these
stores do not type (a character outside the subset falls back to the
system font), and drops the hinting.

The license (SIL OFL 1.1) reserves the name "Plex": a modified version
may not carry it. So the result is renamed "Iftah Sans Arabic" in every
name record, and keeps the copyright notice and the license URL.

Usage (fonttools 4.x):
    scripts/build-iftah-sans.py <dir of the Fontsource files> <out dir>
The inputs are ibm-plex-sans-arabic-arabic-{400,700}.woff2 from
cdn.jsdelivr.net/fontsource/fonts/ibm-plex-sans-arabic@latest/
(arabic-400-normal.woff2, arabic-700-normal.woff2).
"""

import pathlib
import sys

from fontTools import subset
from fontTools.ttLib import TTFont

UNICODES = "U+0600-06FF,U+200C-200F,U+0020-0040,U+00A0,U+2010-2027"
NAME = ("IBM Plex Sans Arabic", "Iftah Sans Arabic")
POSTSCRIPT = ("IBMPlexSansArabic", "IftahSansArabic")
# The records that name the font (family, full, unique, PostScript,
# typographic and WWS names): none may say "Plex". The description (10)
# credits the original by name, as the license asks.
NAME_IDS = (1, 3, 4, 6, 16, 17, 21, 22)
DESCRIPTION = (
    "Iftah Sans Arabic is a modified version (subset, no hinting) of "
    "IBM Plex Sans Arabic by IBM Corp., under the SIL Open Font License 1.1."
)


def build(src: pathlib.Path, out: pathlib.Path) -> None:
    opts = subset.Options()
    opts.layout_features = ["*"]
    opts.hinting = False
    opts.desubroutinize = True
    opts.flavor = "woff2"
    opts.name_IDs = ["*"]
    font = subset.load_font(str(src), opts)
    sub = subset.Subsetter(opts)
    sub.populate(unicodes=subset.parse_unicodes(UNICODES))
    sub.subset(font)
    rename(font)
    subset.save_font(font, str(out), opts)


def rename(font: TTFont) -> None:
    names = font["name"]
    for rec in names.names:
        if rec.nameID in (0, 13, 14):  # copyright and license stay as they are
            continue
        text = rec.toUnicode().replace(*NAME).replace(*POSTSCRIPT)
        names.setName(text, rec.nameID, rec.platformID, rec.platEncID, rec.langID)
    names.setName(DESCRIPTION, 10, 3, 1, 0x409)
    left = [r.nameID for r in names.names if r.nameID in NAME_IDS and "Plex" in r.toUnicode()]
    if left:
        sys.exit(f"name records still say Plex: {left}")


def main() -> None:
    if len(sys.argv) != 3:
        sys.exit(__doc__)
    src, out = pathlib.Path(sys.argv[1]), pathlib.Path(sys.argv[2])
    for weight in ("400", "700"):
        build(src / f"ibm-plex-sans-arabic-arabic-{weight}.woff2", out / f"iftah-sans-arabic-arabic-{weight}.woff2")


if __name__ == "__main__":
    main()
