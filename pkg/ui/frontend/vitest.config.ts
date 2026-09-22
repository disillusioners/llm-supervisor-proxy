import { defineConfig } from 'vitest/config';
import preact from '@preact/preset-vite';

// Minimal Vitest config for the embedded React (Preact) frontend.
// Scoped to happy-dom (lighter than jsdom, sufficient for Preact components).
// Tests live alongside source under src/**/__tests__/*.test.{ts,tsx}.
export default defineConfig({
  plugins: [preact()],
  test: {
    environment: 'happy-dom',
    globals: false,
    include: ['src/**/__tests__/**/*.test.{ts,tsx}'],
    css: false,
  },
});
