# ADR-0072: The site's English under /en, from one set of templates

- Status: Accepted
- Date: 2026-11-02

## Context

The platform's site (ADR-0071) was Arabic only. The approved plan (S3)
adds English for merchants and for search engines and AI assistants
that read English. Two questions:

- where the English pages live;
- how the templates carry two languages without copying every page.

## Decision

1. **Arabic stays at the root; English lives under `/en/`.**
   - The paths are the same: `/pricing` and `/en/pricing`.
   - Arabic is the main market, and its URLs were already published.
   - Every page names both versions with `hreflang` (`ar`, `en`), and
     `x-default` points to the Arabic page.
   - The sitemap lists every page in both languages, each with its
     alternates.
   - The crawler files (`robots.txt`, `sitemap.xml`, `llms.txt`) stay at
     the root only, and `llms.txt` gains an English section.
2. **One set of templates.**
   - A `language` value carries the language code, its direction, the
     path prefix, its short `texts` and its longer `siteCopy`.
   - The templates read every word from these. Nothing is duplicated
     per language, and the `dupl` gate stays quiet.
3. **Prices in English** use billing's English names (`NameEn`) and are
   written the English way: `EGP 299`, `2.5%`.
4. **English pages use the system fonts.** The brand face is Arabic, so
   English pages do not preload it.

## Consequences

- A new page adds its words to both languages, or English shows empty
  text. `TestEveryPage` walks every sitemap page in both languages.
- The console editors (S4) will edit the copy per language.
- A third language would mean a third `language` value and its copy,
  with no template changes.
