// A fetch relay: fetches a target URL server-side from Cloudflare's edge network and
// returns the raw response. Used as one of the backend's fallback tiers (see
// backend/internal/extractor/cfrelay.go) when our own IP gets blocked by a site's bot
// protection — a different network origin is often enough to get through, without the
// cost of spinning up a headless browser.
//
// This is a plain HTTP fetch relay, not a SOCKS5/CONNECT proxy: it can't tunnel arbitrary
// TCP, and it doesn't execute JavaScript (a JS-challenge page will still come back as a
// challenge page — only IP/TLS-fingerprint-based blocks are addressed by this).
//
// Gated by a shared-secret header so this can't be discovered and used as an open relay
// by anyone else. Set the secret with:
//   wrangler secret put RELAY_TOKEN

export default {
  async fetch(request, env) {
    if (request.method !== 'GET') {
      return json({ error: 'method not allowed' }, 405);
    }

    const auth = request.headers.get('X-Relay-Token');
    if (!env.RELAY_TOKEN || auth !== env.RELAY_TOKEN) {
      return json({ error: 'unauthorized' }, 401);
    }

    const requestUrl = new URL(request.url);
    const target = requestUrl.searchParams.get('url');
    if (!target) {
      return json({ error: 'missing url param' }, 400);
    }

    let targetUrl;
    try {
      targetUrl = new URL(target);
    } catch {
      return json({ error: 'invalid url' }, 400);
    }
    if (targetUrl.protocol !== 'https:' && targetUrl.protocol !== 'http:') {
      return json({ error: 'unsupported protocol' }, 400);
    }

    let upstream;
    try {
      upstream = await fetch(targetUrl.toString(), {
        method: 'GET',
        headers: {
          'User-Agent':
            request.headers.get('X-Relay-User-Agent') ||
            'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36',
          Accept:
            'text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8',
          'Accept-Language': request.headers.get('X-Relay-Accept-Language') || 'en-US,en;q=0.9',
        },
        redirect: 'follow',
        cf: { cacheTtl: 0, cacheEverything: false },
      });
    } catch (err) {
      return json({ error: 'upstream fetch failed', message: String(err) }, 502);
    }

    const body = await upstream.arrayBuffer();
    return new Response(body, {
      status: upstream.status,
      headers: {
        'Content-Type': upstream.headers.get('Content-Type') || 'text/html; charset=utf-8',
        'X-Relay-Final-URL': upstream.url,
      },
    });
  },
};

function json(data, status) {
  return new Response(JSON.stringify(data), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}
