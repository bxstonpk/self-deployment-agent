/// <reference types="vitest/config" />
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    globals: true,
    // 'forks' (the default) fails to spawn child processes at all in this
    // sandboxed environment ("Failed to start forks worker", 0 tests ever
    // run despite an exit code of 0 — worth knowing about since that
    // combination looks like a pass at a glance). 'threads' uses
    // worker_threads instead of child_process and works here.
    pool: 'threads',
  },
})
