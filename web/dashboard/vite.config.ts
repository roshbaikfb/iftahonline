/// <reference types="vitest/config" />
import react from '@vitejs/plugin-react';
import { defineConfig, type Plugin } from 'vite';

// The API runs separately (make dev); /v1 is proxied so the dashboard and
// API share an origin, as they will behind the production reverse proxy.
const apiTarget = process.env.VITE_API_PROXY ?? 'http://127.0.0.1:18080';
const proxy = { '/v1': { target: apiTarget, changeOrigin: false }, '/admin/v1': { target: apiTarget, changeOrigin: false } };

// The console (7f-3b) is a second page, admin.html, served under /console
// on the console's host; dev and preview send /console/* to it.
const consolePage: Plugin = {
  name: 'console-page',
  configureServer(server) {
    server.middlewares.use((req, _res, next) => {
      if (req.url?.startsWith('/console')) req.url = '/admin.html';
      next();
    });
  },
  configurePreviewServer(server) {
    server.middlewares.use((req, _res, next) => {
      if (req.url?.startsWith('/console')) req.url = '/admin.html';
      next();
    });
  },
};

// The production build has no inline scripts, so preview (and production)
// can use a strict CSP. Dev mode is exempt: Vite injects inline HMR code.
const securityHeaders = {
  'Content-Security-Policy':
    "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'",
  'X-Content-Type-Options': 'nosniff',
  'Referrer-Policy': 'strict-origin-when-cross-origin',
  'X-Frame-Options': 'DENY',
};

export default defineConfig({
  plugins: [react(), consolePage],
  // The design screen shows the shop's own font files (shop-fonts.css).
  server: { port: 5173, strictPort: true, proxy, fs: { allow: ['.', '../../internal/modules/storefront/internal/web/assets/fonts'] } },
  preview: { port: 4173, strictPort: true, proxy, headers: securityHeaders },
  build: { sourcemap: true, rollupOptions: { input: { main: 'index.html', console: 'admin.html' } } },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    include: ['src/**/*.test.{ts,tsx}'],
    restoreMocks: true,
  },
});
