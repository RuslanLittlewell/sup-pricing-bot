#!/bin/sh
set -eu

if [ -n "${SCRAPER_EXTENSION_DIR:-}" ]; then
    export DISPLAY="${DISPLAY:-:99}"
    Xvfb "$DISPLAY" -screen 0 1920x1080x24 -nolisten tcp >/tmp/xvfb.log 2>&1 &
fi

exec "$@"
