package proxypool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// geonodeListURL is geonode.com's free public proxy list, sorted by response time so the
// fastest candidates are checked first. See https://geonode.com/free-proxy-list for the
// human-facing version of this same data.
const geonodeListURL = "https://proxylist.geonode.com/api/proxy-list?protocols=https%2Csocks5&page=1&limit=500&sort_by=responseTime&sort_type=asc&google=true"

type geonodeEntry struct {
	IP        string   `json:"ip"`
	Port      string   `json:"port"`
	Country   string   `json:"country"`
	Protocols []string `json:"protocols"`
}

type geonodeResponse struct {
	Data []geonodeEntry `json:"data"`
}

func fetchGeonodeList(ctx context.Context) ([]geonodeEntry, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, geonodeListURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build geonode request: %w", err)
	}

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("geonode request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("geonode returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read geonode response: %w", err)
	}

	var parsed geonodeResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse geonode response: %w", err)
	}
	return parsed.Data, nil
}

// preferredProtocol picks which protocol to actually use for an entry that advertises
// several — our infra can drive either socks5 (CheckAlive) or http (CheckAliveHTTP);
// socks5 wins when both are offered since it's the pool's original, more-exercised path,
// but most geonode entries only offer http/https, which is still fully usable via CONNECT.
func preferredProtocol(protocols []string) (string, bool) {
	hasSocks5, hasHTTP := false, false
	for _, p := range protocols {
		switch p {
		case "socks5":
			hasSocks5 = true
		case "http", "https":
			hasHTTP = true
		}
	}
	switch {
	case hasSocks5:
		return "socks5", true
	case hasHTTP:
		return "http", true
	default:
		return "", false
	}
}

// FetchGeonode pulls the current free public proxy list from geonode.com, keeps only
// entries not already in the pool, verifies each candidate actually completes a live
// HTTPS handshake (geonode's own upTime/lastChecked fields are self-reported against
// geonode's own network path, not ours — plenty of "alive" entries turn out dead here),
// and adds only the ones that pass. Meant to be called on an hourly schedule (see
// cmd/worker); logs and returns past any failure rather than panicking the caller's loop.
func (s *Store) FetchGeonode(ctx context.Context, log zerolog.Logger) {
	entries, err := fetchGeonodeList(ctx)
	if err != nil {
		log.Error().Err(err).Msg("proxypool: failed to fetch geonode proxy list")
		return
	}

	existing, err := s.existingAddresses(ctx)
	if err != nil {
		log.Error().Err(err).Msg("proxypool: failed to list existing proxy addresses")
		return
	}

	type candidate struct {
		address, protocol, country string
	}
	var candidates []candidate
	for _, e := range entries {
		address := e.IP + ":" + e.Port
		if existing[address] {
			continue
		}
		protocol, ok := preferredProtocol(e.Protocols)
		if !ok {
			continue
		}
		candidates = append(candidates, candidate{address: address, protocol: protocol, country: e.Country})
	}

	sem := make(chan struct{}, maxAliveCheckConcurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var alive []DiscoveredEntry

	for _, c := range candidates {
		wg.Add(1)
		sem <- struct{}{}
		go func(c candidate) {
			defer wg.Done()
			defer func() { <-sem }()

			checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()

			var ok bool
			if c.protocol == "socks5" {
				ok = CheckAlive(checkCtx, c.address, nil)
			} else {
				ok = CheckAliveHTTP(checkCtx, c.address, "", "")
			}
			if !ok {
				return
			}

			mu.Lock()
			alive = append(alive, DiscoveredEntry{Address: c.address, Protocol: c.protocol, CountryCode: c.country, Source: "geonode"})
			mu.Unlock()
		}(c)
	}
	wg.Wait()

	added, err := s.addDiscovered(ctx, alive)
	if err != nil {
		log.Error().Err(err).Msg("proxypool: failed to save discovered geonode proxies")
		return
	}
	log.Info().
		Int("fetched", len(entries)).
		Int("new_candidates", len(candidates)).
		Int("handshake_ok", len(alive)).
		Int("added", added).
		Msg("proxypool: geonode fetch complete")
}
