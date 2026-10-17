import createClient from 'openapi-fetch';

import type { paths } from './schema';

// Typed client generated from api/openapi.yaml (`make gen`). Same-origin:
// the dev server and the production reverse proxy route /v1 to the API.
// fetch is looked up per call (not captured at import) so tests can stub it.
export const api = createClient<paths>({
  baseUrl: globalThis.location.origin,
  fetch: (request) => globalThis.fetch(request),
});

export type { components } from './schema';
