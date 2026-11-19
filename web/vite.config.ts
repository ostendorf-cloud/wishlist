import { fileURLToPath, URL } from 'node:url'

import tailwindcss from '@tailwindcss/vite'
import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vitest/config'

// cmd/wishlist embeds dist/ with a go:embed directive that resolves next to
// the Go source file, which is why the build writes there directly rather
// than into web/dist and leaving a copy step for the Taskfile to do.
export default defineConfig({
  plugins: [vue(), tailwindcss()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  // The prerender bundle (src/prerender.ts) runs in plain Node, which cannot
  // load govue's TypeScript sources, so vite compiles the package into it.
  ssr: { noExternal: ['@ostendorf-cloud/govue'] },
  build: {
    outDir: '../cmd/wishlist/dist',
    emptyOutDir: true,
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./vitest.setup.ts'],
    css: false,
  },
})
