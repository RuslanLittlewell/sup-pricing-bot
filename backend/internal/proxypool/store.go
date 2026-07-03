// Package proxypool maintains a pool of scraping proxies: authenticated entries added
// manually (see AddManual — e.g. from a paid rotating-proxy provider), periodically
// re-checked for aliveness. It's a building block: it stores and hands out working
// proxies — wiring an actual scrape (fetcher.go/renderer.go) to draw from it is a
// separate, later step.
package proxypool

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	xproxy "golang.org/x/net/proxy"
)

// maxAliveCheckConcurrency bounds how many SOCKS5 handshakes run at once during a
// refresh — checking every pooled proxy serially would make each hourly run slow as the
// pool grows.
const maxAliveCheckConcurrency = 20

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Proxy is one row of the pool, as surfaced to callers (admin listing). Never carries a
// password — see PickedProxy for the form actually used to make requests.
type Proxy struct {
	ID            string
	Address       string
	Protocol      string
	CountryCode   string
	Status        string
	Source        string
	HasAuth       bool
	UseCount      int
	LastCheckedAt *time.Time
	LastUsedAt    *time.Time
	CreatedAt     time.Time
}

// ManualEntry is an authenticated proxy added directly, e.g. from a paid rotating-proxy
// provider's IP list.
type ManualEntry struct {
	Address     string
	Username    string
	Password    string
	CountryCode string
}

// PickedProxy is what Pick hands back: enough to actually dial through the proxy.
type PickedProxy struct {
	Address  string
	Username string
	Password string
}

// URL renders the proxy as a socks5:// URL suitable for an HTTP client/renderer's proxy
// config, embedding credentials only when the proxy has them.
func (p PickedProxy) URL() string {
	if p.Username == "" {
		return "socks5://" + p.Address
	}
	return fmt.Sprintf("socks5://%s:%s@%s", url.QueryEscape(p.Username), url.QueryEscape(p.Password), p.Address)
}

// Refresh re-checks aliveness of every proxy currently in the pool (a previously-dead
// proxy can come back, and a previously-alive one can have gone down since the last
// check). Meant to be called on a schedule (see cmd/worker); logs and continues past
// individual failures rather than aborting the whole run.
func (s *Store) Refresh(ctx context.Context, log zerolog.Logger) {
	s.checkAllAliveness(ctx, log)
}

// AddManual inserts (or refreshes the country/credentials of) a list of authenticated
// proxies, e.g. from a paid rotating-proxy provider — the pool's only source of new
// proxies (see package doc). Always overwrites username/password on conflict —
// deliberately, since a re-add of the same address is how you'd rotate in fresh
// credentials for it.
func (s *Store) AddManual(ctx context.Context, entries []ManualEntry) error {
	for _, e := range entries {
		if _, err := s.pool.Exec(ctx, `
			INSERT INTO proxies (address, protocol, country_code, username, password, source)
			VALUES ($1, 'socks5', NULLIF($2, ''), NULLIF($3, ''), NULLIF($4, ''), 'manual')
			ON CONFLICT (address) DO UPDATE SET
				country_code = COALESCE(NULLIF($2, ''), proxies.country_code),
				username = NULLIF($3, ''),
				password = NULLIF($4, ''),
				source = 'manual',
				updated_at = now()
		`, e.Address, e.CountryCode, e.Username, e.Password); err != nil {
			return fmt.Errorf("add manual proxy %s: %w", e.Address, err)
		}
	}
	return nil
}

func (s *Store) checkAllAliveness(ctx context.Context, log zerolog.Logger) {
	rows, err := s.pool.Query(ctx, `SELECT id, address, username, password FROM proxies`)
	if err != nil {
		log.Error().Err(err).Msg("proxypool: failed to list proxies for aliveness check")
		return
	}
	type entry struct {
		id, address        string
		username, password *string
	}
	var all []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.id, &e.address, &e.username, &e.password); err != nil {
			continue
		}
		all = append(all, e)
	}
	rows.Close()

	sem := make(chan struct{}, maxAliveCheckConcurrency)
	var wg sync.WaitGroup
	var aliveCount, deadCount int
	var mu sync.Mutex

	for _, e := range all {
		wg.Add(1)
		sem <- struct{}{}
		go func(e entry) {
			defer wg.Done()
			defer func() { <-sem }()

			var auth *xproxy.Auth
			if e.username != nil && *e.username != "" {
				password := ""
				if e.password != nil {
					password = *e.password
				}
				auth = &xproxy.Auth{User: *e.username, Password: password}
			}

			checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			alive := CheckAlive(checkCtx, e.address, auth)
			cancel()

			status := "dead"
			if alive {
				status = "alive"
			}
			mu.Lock()
			if alive {
				aliveCount++
			} else {
				deadCount++
			}
			mu.Unlock()

			s.pool.Exec(ctx, `
				UPDATE proxies SET status = $2, last_checked_at = now(), updated_at = now()
				WHERE id = $1
			`, e.id, status)
		}(e)
	}
	wg.Wait()

	log.Info().Int("alive", aliveCount).Int("dead", deadCount).Msg("proxypool: aliveness check complete")
}

// Pick returns a currently-alive proxy, chosen at random, and records the pick
// (use_count, last_used_at) so the admin dashboard can show which proxies are actually
// carrying traffic. Returns ok=false if the pool has no alive proxy right now.
func (s *Store) Pick(ctx context.Context) (picked PickedProxy, ok bool) {
	var username, password *string
	err := s.pool.QueryRow(ctx, `
		UPDATE proxies SET use_count = use_count + 1, last_used_at = now(), updated_at = now()
		WHERE id = (
			SELECT id FROM proxies WHERE status = 'alive' ORDER BY random() LIMIT 1
		)
		RETURNING address, username, password
	`).Scan(&picked.Address, &username, &password)
	if err != nil {
		return PickedProxy{}, false
	}
	if username != nil {
		picked.Username = *username
	}
	if password != nil {
		picked.Password = *password
	}
	return picked, true
}

// List returns every proxy in the pool for the admin dashboard, most recently used first.
// Never returns a password — HasAuth just indicates whether the proxy is authenticated.
func (s *Store) List(ctx context.Context) ([]Proxy, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, address, protocol, COALESCE(country_code, ''), status, source,
		       (username IS NOT NULL AND username != ''), use_count,
		       last_checked_at, last_used_at, created_at
		FROM proxies
		ORDER BY last_used_at DESC NULLS LAST, created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	proxies := []Proxy{}
	for rows.Next() {
		var p Proxy
		if err := rows.Scan(&p.ID, &p.Address, &p.Protocol, &p.CountryCode, &p.Status, &p.Source,
			&p.HasAuth, &p.UseCount, &p.LastCheckedAt, &p.LastUsedAt, &p.CreatedAt); err != nil {
			return nil, err
		}
		proxies = append(proxies, p)
	}
	return proxies, rows.Err()
}
