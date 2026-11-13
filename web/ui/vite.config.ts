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
    server: { proxy: { '/api': api } },
  };
});
