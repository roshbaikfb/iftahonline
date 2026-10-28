// The server's own section schemas, so editors are tested against what
// the API serves (GET …/storefront/section-types).
const files = import.meta.glob<Record<string, unknown>>('../../../../internal/modules/storefront/internal/sections/schemas/*.json', { eager: true, import: 'default' });

export const sectionTypeList = Object.entries(files)
  .map(([path, schema]) => ({ type: path.replace(/^.*\/|\.json$/g, ''), schema }))
  .filter((t) => t.type !== 'defs');
