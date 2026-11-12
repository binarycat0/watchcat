import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// The build goes to web/dist/app, which the Go binary embeds (web/embed.go).
// `npm run dev` proxies the API to a running daemon for live UI work.
export default defineConfig({
  plugins: [react()],
  base: './',
  build: { outDir: '../dist/app', emptyOutDir: true },
  server: { proxy: { '/api': 'http://127.0.0.1:7878' } },
});
