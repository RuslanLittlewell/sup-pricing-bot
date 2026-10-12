#!/bin/sh
set -eu

# A headful Chromium (SCRAPER_HEADFUL, or the diagnostic extension, which needs one) draws
# into this virtual display.
if [ "${SCRAPER_HEADFUL:-}" = "true" ] || [ -n "${SCRAPER_EXTENSION_DIR:-}" ]; then
    export DISPLAY="${DISPLAY:-:99}"
    Xvfb "$DISPLAY" -screen 0 1920x1080x24 -nolisten tcp >/tmp/xvfb.log 2>&1 &
fi

exec "$@"
