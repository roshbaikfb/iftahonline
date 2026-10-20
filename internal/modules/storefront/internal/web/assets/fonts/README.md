# Storefront fonts

Self-hosted so the CSP can keep `font-src 'self'` and shoppers' browsers
never call a third party (ADR-0018). Each family has weights 400 and
700, in two subsets (Arabic, Latin) selected with `unicode-range`.

| Family | Files | License |
|---|---|---|
| Cairo | `cairo-*.woff2` | SIL OFL 1.1 (`licenses/OFL-cairo.txt`) |
| Tajawal | `tajawal-*.woff2` | SIL OFL 1.1 (`licenses/OFL-tajawal.txt`) |
| IBM Plex Sans Arabic | `ibm-plex-sans-arabic-*.woff2` | SIL OFL 1.1 (`licenses/OFL-ibmplexsansarabic.txt`) |
| Noto Kufi Arabic | `noto-kufi-arabic-*.woff2` | SIL OFL 1.1 (`licenses/OFL-notokufiarabic.txt`) |

Source: the Fontsource builds of the Google Fonts releases
(`cdn.jsdelivr.net/fontsource/fonts/<family>@latest/<subset>-<weight>-normal.woff2`),
fetched 2026-10-20; licenses from `github.com/google/fonts/ofl/<family>`.
