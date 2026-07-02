// @ts-check
import { defineConfig } from 'astro/config';
import node from '@astrojs/node';
import react from '@astrojs/react';
import sitemap from '@astrojs/sitemap';

export default defineConfig({
  site: 'https://pricebot.littlewell-app.work',
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
    }),
  ]
});
