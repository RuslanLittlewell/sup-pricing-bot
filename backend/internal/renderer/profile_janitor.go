package renderer

import (
	"context"
	"time"

	"github.com/rs/zerolog"
)

// ProfilePruneInterval is both how often the janitor runs and how long a profile must have
// gone unused before it may be deleted.
const ProfilePruneInterval = 48 * time.Hour

// RunProfileJanitor deletes, now and then every ProfilePruneInterval, the browser profiles
// of hosts that no longer have an active tracker (see PruneProfiles). activeURLs returns
// the URLs of the trackers that are still being checked; it blocks until ctx is done.
func (r *Renderer) RunProfileJanitor(ctx context.Context, activeURLs func(context.Context) ([]string, error), log zerolog.Logger) {
	prune := func() {
		urls, err := activeURLs(ctx)
		if err != nil {
			// Never prune on a failed lookup: an empty keep-set would read as "no host is
			// tracked any more" and take every idle profile with it.
			if ctx.Err() == nil {
				log.Error().Err(err).Msg("browser profile cleanup skipped: failed to list active trackers")
			}
			return
		}
		keep := make(map[string]bool, len(urls))
		for _, rawURL := range urls {
			if key, ok := ProfileKey(rawURL); ok {
				keep[key] = true
			}
		}
		removed, err := r.PruneProfiles(keep, ProfilePruneInterval)
		if err != nil {
			log.Error().Err(err).Msg("browser profile cleanup: some profiles could not be removed")
		}
		if len(removed) > 0 {
			log.Info().Strs("profiles", removed).Msg("removed browser profiles of hosts without active trackers")
		}
	}

	prune()
	ticker := time.NewTicker(ProfilePruneInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			prune()
		case <-ctx.Done():
			return
		}
	}
}
