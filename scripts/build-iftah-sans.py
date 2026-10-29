#!/usr/bin/env python3
"""Builds the storefront's Iftah Sans Arabic from IBM Plex Sans Arabic.

The full IBM Plex Sans Arabic is ten times the size the storefront can
afford on a slow phone network. This keeps what Egyptian and Gulf stores
write, in one file per weight:

- the Arabic letters, marks and digits these stores type, with the four
  Persian letters used for foreign sounds (U+067E, U+0686, U+06A4,
  U+06AF), the Persian keyboard's kaf, yeh and digits;
- ASCII, the Latin-1 symbols, dashes, quotes, the euro, the minus and the
  joiners, so an Arabic page (prices, codes, a brand name in English)
  needs no Latin file at all;
- every OpenType layout feature, so shaping is unchanged.

A character outside the subset falls to the family's Latin file (if it
is there) or to the system font. The hinting is dropped.

The license (SIL OFL 1.1) reserves the name "Plex": a modified version
may not carry it. So the result is renamed "Iftah Sans Arabic" in every
name record, and keeps the copyright notice and the license URL.

Usage (fonttools 4.x):
    scripts/build-iftah-sans.py <dir of the source files> <out dir>
The inputs are IBMPlexSansArabic-{Regular,Bold}.ttf from
github.com/google/fonts/tree/main/ofl/ibmplexsansarabic.
"""

import pathlib
import sys

from fontTools import subset
from fontTools.ttLib import TTFont

UNICODES = (
    "U+060C,U+061B,U+061F,U+0621-063A,U+0640-0655,U+0660-066D,U+0670,"
    "U+067E,U+0686,U+06A4,U+06A9,U+06AF,U+06CC,U+06D4,U+06F0-06F9,"
    "U+0020-007E,U+00A0-00BF,U+00D7,U+00F7,U+200C-200F,U+2010-2027,"
    "U+2030-203A,U+20AC,U+2122,U+2212"
)
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
    for weight, style in (("400", "Regular"), ("700", "Bold")):
        build(src / f"IBMPlexSansArabic-{style}.ttf", out / f"iftah-sans-arabic-arabic-{weight}.woff2")


if __name__ == "__main__":
    main()
