# Storefront fonts

Self-hosted so the CSP can keep `font-src 'self'` and shoppers' browsers
never call a third party (ADR-0018). Each family has weights 400 and
700, in two subsets (Arabic, Latin) selected with `unicode-range`.

| Family | Files | License |
|---|---|---|
| Cairo | `cairo-*.woff2` | SIL OFL 1.1 (`licenses/OFL-cairo.txt`) |
| Tajawal | `tajawal-*.woff2` | SIL OFL 1.1 (`licenses/OFL-tajawal.txt`) |
| Iftah Sans Arabic (from IBM Plex Sans Arabic) | `iftah-sans-arabic-*.woff2` | SIL OFL 1.1 (`licenses/OFL-ibmplexsansarabic.txt`) |
| Noto Kufi Arabic | `noto-kufi-arabic-*.woff2` | SIL OFL 1.1 (`licenses/OFL-notokufiarabic.txt`) |

Source: the Fontsource builds of the Google Fonts releases
(`cdn.jsdelivr.net/fontsource/fonts/<family>@latest/<subset>-<weight>-normal.woff2`),
fetched 2026-10-20; licenses from `github.com/google/fonts/ofl/<family>`.

## Iftah Sans Arabic

The theme font `ibm-plex-sans-arabic` is served as Iftah Sans Arabic.

- **Arabic files.** They are IBM Plex Sans Arabic (© IBM Corp.), modified
  by `scripts/build-iftah-sans.py` from the Google Fonts release
  (`github.com/google/fonts/tree/main/ofl/ibmplexsansarabic`, fetched
  2026-10-29). The script:
  - keeps the Arabic letters, marks and digits Egyptian and Gulf stores
    type, ASCII and the common symbols, and every layout feature;
  - drops the rest of the Arabic block, the presentation forms and the
    hinting.

  Each weight is 24 KB. Because the Arabic files hold ASCII, a page in
  either language needs only these two files (the stylesheet declares
  the Arabic face after the Latin one, so it wins where they overlap,
  and both are preloaded): a phone on a slow network fetches two fonts,
  not four, before the largest paint.
- **The name.** The license reserves the name "Plex" for the original,
  so the modified fonts are named "Iftah Sans Arabic" in every name
  record. They keep IBM's copyright notice and the license, and credit
  the original in their description.
- **Latin files.** They are Fontsource's unmodified files, renamed on
  disk to share the family's prefix.
- **What is outside the subset.** A Latin character outside ASCII (an
  accented letter) comes from the Latin file; any other character falls
  back to the system font.
