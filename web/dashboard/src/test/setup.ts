import '@testing-library/jest-dom/vitest';
import { cleanup } from '@testing-library/react';
import { afterEach, vi } from 'vitest';

// Outside a test's mockAPI, fetch never answers. A screen still loading
// when its test ends sends requests after that test's mock is restored;
// with the real fetch they went to jsdom's localhost:3000 and failed the
// run at random (ECONNREFUSED). Now nothing leaves the process, and the
// unmounted screen simply never hears back.
vi.stubGlobal('fetch', () => new Promise<Response>(() => undefined));

afterEach(() => {
  cleanup();
});
