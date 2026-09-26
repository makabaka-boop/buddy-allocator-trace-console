import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

// In dev the browser calls /api/* and Vite proxies to the Go allocator.
// In `vite preview` the same proxy block applies.
export default defineConfig({
  plugins: [svelte()],
  server: {
    proxy: {
      '/api': {
        target: process.env.ALLOCATOR_URL || 'http://localhost:8080',
        changeOrigin: true
      }
    }
  },
  preview: {
    proxy: {
      '/api': {
        target: process.env.ALLOCATOR_URL || 'http://localhost:8080',
        changeOrigin: true
      }
    }
  }
});
