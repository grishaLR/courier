// @ts-check
import { defineConfig } from 'astro/config';

// https://astro.build/config
export default defineConfig({
  image: {
    // Remote favicons can't be optimized at build time
    remotePatterns: [{ protocol: "https" }],
  },
});
