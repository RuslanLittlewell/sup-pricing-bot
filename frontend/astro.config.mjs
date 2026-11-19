// @ts-check
import { defineConfig } from 'astro/config';
import node from '@astrojs/node';
import react from '@astrojs/react';
import sitemap from '@astrojs/sitemap';

export default defineConfig({
  site: 'https://surpricebot.com',
  adapter: node({
    mode: 'standalone'
  }),
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
