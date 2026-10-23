import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// The platform serves this app under /run/{application}/frontend/, a
// prefix the build can't know in advance — so every asset path is relative
// (base: './'). The platform's build engine serves the `build/` directory.
export default defineConfig({
  plugins: [react()],
  base: './',
  build: { outDir: 'build' },
  server: {
    // Local development: `npm run dev` here, the API on localhost:8080.
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        rewrite: (path) => path.replace(/^\/api/, ''),
      },
    },
  },
})
