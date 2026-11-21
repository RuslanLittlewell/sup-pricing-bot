# Local development image only (see the root docker-compose.yml, which runs
# `astro dev` on top of it). Production is a static build on Cloudflare Pages:
# `npm run deploy`.
FROM node:22-alpine AS builder
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci
COPY . .
