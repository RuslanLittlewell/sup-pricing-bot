// @ts-check
import { defineConfig } from 'astro/config';
import react from '@astrojs/react';
import sitemap from '@astrojs/sitemap';

// Fully static build (no adapter): `dist/` is deployed as-is to Cloudflare Pages,
// see wrangler.jsonc and the `deploy` script.
export default defineConfig({
  site: 'https://surpricebot.com',
  server: {
    port: 3000,
    host: '0.0.0.0'
  },
  integrations: [
    react(),
    sitemap({
      filter: (page) => !page.includes('/404'),
      // Adds <xhtml:link hreflang> alternates linking "/", "/ru/" and "/pl/".
      i18n: {
        defaultLocale: 'en',
        locales: { en: 'en', ru: 'ru', pl: 'pl' },
      },
    }),
  ]
});
