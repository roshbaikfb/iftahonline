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
  by `scripts/build-iftah-sans.py` from the Fontsource files above. The
  script:
  - keeps the Arabic block, the joiners, punctuation and digits, and
    every layout feature;
  - drops the Arabic Supplement, the presentation forms and the hinting.

  That takes each weight from 43 KB to 22 KB, which keeps the
  storefront's LCP within its 1.5 s budget on a slow phone network.
- **The name.** The license reserves the name "Plex" for the original,
  so the modified fonts are named "Iftah Sans Arabic" in every name
  record. They keep IBM's copyright notice and the license, and credit
  the original in their description.
- **Latin files.** They are Fontsource's unmodified files, renamed on
  disk to share the family's prefix.
- **What is outside the subset.** A character outside the subset (rare
  in Egyptian and Gulf stores) falls back to the system font.
