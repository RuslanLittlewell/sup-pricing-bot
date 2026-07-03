# Cloudflare fetch relay

A minimal Cloudflare Worker that fetches a target URL server-side (from Cloudflare's
edge network) and returns the raw response. Used by the backend as a fallback tier when
our own server's IP gets blocked — see `backend/internal/extractor/cfrelay.go`.

Not a SOCKS5/CONNECT proxy — it's an HTTP fetch relay. It doesn't execute JavaScript, so
it only helps against IP/TLS-fingerprint-based blocks, not JS challenge pages (Cloudflare
Turnstile, etc.) — those still need the headless-render fallback.

## Deploy

```sh
cd cloudflare-relay
npx wrangler login          # first time only
npx wrangler secret put RELAY_TOKEN   # paste a long random value when prompted
npx wrangler deploy
```

This prints the deployed URL, e.g. `https://price-tracker-relay.<your-subdomain>.workers.dev`.

## Wire it into the backend

Set these on the backend/worker services (`.env` locally, `.env.production` / the deploy
script on the server):

```
CF_RELAY_URL=https://price-tracker-relay.<your-subdomain>.workers.dev
CF_RELAY_TOKEN=<the same value you set with `wrangler secret put`>
```

Both must be set for the relay tier to activate — see `extractor.NewCloudflareRelay()`. If
either is empty, `PageFetcher.Fetch` just skips this tier and falls straight to the
headless renderer, same as before this existed.

## Test it directly

```sh
curl -s -H "X-Relay-Token: <token>" \
  "https://price-tracker-relay.<your-subdomain>.workers.dev/?url=https://example.com" | head -c 200
```
