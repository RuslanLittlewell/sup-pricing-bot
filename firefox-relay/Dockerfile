FROM python:3.12-slim-bookworm

# Camoufox: a Firefox build whose fingerprint (navigator, screen, fonts, WebGL, WebRTC,
# timezone) is spoofed inside the engine rather than through injected JS, so there is no
# patched-property trail for Akamai/DataDome to find. The Python package, its Playwright
# and the browser build are all pinned: `camoufox fetch` without a version installs
# whatever build is newest at image-build time.
RUN pip install --no-cache-dir "camoufox[geoip]==0.5.6" "playwright==1.62.0" \
    && playwright install-deps firefox \
    && apt-get install -y --no-install-recommends xvfb \
    && python -m camoufox fetch official/152.0.4-beta.30 \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY server.py /app/server.py

EXPOSE 8080
CMD ["python", "/app/server.py"]
