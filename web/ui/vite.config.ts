import { defineConfig, loadEnv } from 'vite';
import react from '@vitejs/plugin-react';

// The build goes to web/dist/app, which the Go binary embeds (web/embed.go).
// `npm run dev` proxies the API to a running daemon for live UI work;
// set WATCHCAT_API if the daemon isn't on the default host/port.
export default defineConfig(({ mode }) => {
  const api = loadEnv(mode, '.', 'WATCHCAT_').WATCHCAT_API || 'http://127.0.0.1:7878';
  return {
    plugins: [react()],
    base: './',
    build: { outDir: '../dist/app', emptyOutDir: true },
    server: {
      proxy: {
        '/api': {
          target: api,
          changeOrigin: true,
          // The daemon rejects writes whose Origin differs from its Host.
          // Requests from this dev page carry the dev server's origin, so
          // present them as same-origin. Other origins pass through as-is
          // and are still rejected.
          configure: (proxy) => {
            proxy.on('proxyReq', (proxyReq, req) => {
              const origin = req.headers.origin;
              if (origin && new URL(origin).host === req.headers.host) {
                proxyReq.setHeader('origin', new URL(api).origin);
              }
            });
          },
        },
      },
    },
  };
});
