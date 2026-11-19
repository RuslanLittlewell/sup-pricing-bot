#!/bin/sh
set -eu

# A headful Chromium (SCRAPER_HEADFUL, or the diagnostic extension, which needs one) draws
# into this virtual display.
if [ "${SCRAPER_HEADFUL:-}" = "true" ] || [ -n "${SCRAPER_EXTENSION_DIR:-}" ]; then
    export DISPLAY="${DISPLAY:-:99}"
    # A container that is restarted (host reboot, docker restart) rather than recreated
    # keeps its /tmp, so the previous Xvfb's lock and socket are still there. Xvfb then
    # refuses to start ("Server is already active for display 99") and every headful
    # Chromium launch fails with "Missing X server or $DISPLAY". Nothing else in this
    # container can own the display at this point, so the leftovers are always stale.
    display_num="${DISPLAY#*:}"
    display_num="${display_num%%.*}"
    rm -f "/tmp/.X${display_num}-lock" "/tmp/.X11-unix/X${display_num}"
    Xvfb "$DISPLAY" -screen 0 1920x1080x24 -nolisten tcp >/tmp/xvfb.log 2>&1 &
fi

exec "$@"
