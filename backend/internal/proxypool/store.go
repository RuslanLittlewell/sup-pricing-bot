// Package proxypool maintains a pool of scraping proxies: authenticated entries added
// manually (see AddManual — e.g. from a paid rotating-proxy provider), periodically
// re-checked for aliveness. It's a building block: it stores and hands out working
// proxies — wiring an actual scrape (fetcher.go/renderer.go) to draw from it is a
// separate, later step.
package proxypool

import (
	"context"
	"fmt"
	"hash/fnv"
	"net/url"
	"strings"
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
	NetworkType   string
	Provider      string
	ASN           string
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
	NetworkType string
	Provider    string
	ASN         string
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
	return p.urlWithScheme("socks5")
}

// HTTPURL renders the proxy as an http:// URL instead of socks5:// — needed for OpenSERP
// specifically: it rejects authenticated socks5/socks5h proxies outright ("authenticated
// SOCKS proxies are not supported in browser mode", since Chrome/CDP has no clean way to
// answer a SOCKS5 auth challenge), but accepts http:// with embedded credentials fine.
// Only use this where the target is known to accept HTTP-CONNECT-with-auth on the same
// port a SOCKS5 dial would use — true of the rotating-proxy provider this pool currently
// holds, not guaranteed for every proxy in general.
func (p PickedProxy) HTTPURL() string {
	return p.urlWithScheme("http")
}

func (p PickedProxy) urlWithScheme(scheme string) string {
	if p.Username == "" {
		return scheme + "://" + p.Address
	}
	return fmt.Sprintf("%s://%s:%s@%s", scheme, url.QueryEscape(p.Username), url.QueryEscape(p.Password), p.Address)
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
			INSERT INTO proxies (address, protocol, country_code, username, password, source, network_type, provider, asn)
			VALUES ($1, 'socks5', NULLIF($2, ''), NULLIF($3, ''), NULLIF($4, ''), 'manual', $5, NULLIF($6, ''), NULLIF($7, ''))
			ON CONFLICT (address) DO UPDATE SET
				country_code = COALESCE(NULLIF($2, ''), proxies.country_code),
				username = NULLIF($3, ''),
				password = NULLIF($4, ''),
				network_type = $5,
				provider = COALESCE(NULLIF($6, ''), proxies.provider),
				asn = COALESCE(NULLIF($7, ''), proxies.asn),
				source = 'manual',
				updated_at = now()
		`, e.Address, e.CountryCode, e.Username, e.Password, normalizeNetworkType(e.NetworkType), e.Provider, e.ASN); err != nil {
			return fmt.Errorf("add manual proxy %s: %w", e.Address, err)
		}
	}
	return nil
}

func normalizeNetworkType(value string) string {
	switch value {
	case "residential", "mobile", "datacenter":
		return value
	default:
		return "unknown"
	}
}

// DiscoveredEntry is a free/public proxy found by an automatic source (see FetchGeonode) —
// unauthenticated, and only ever inserted once: an address already in the pool is left
// untouched rather than refreshed, unlike AddManual's paid-provider credential rotation
// (re-discovering the same address on a public list tells us nothing new).
type DiscoveredEntry struct {
	Address     string
	Protocol    string // "socks5" or "http"
	CountryCode string
	Source      string
}

// existingAddresses lists every address currently in the pool, for de-duplicating a
// freshly-fetched public proxy list before checking (or storing) any of it.
func (s *Store) existingAddresses(ctx context.Context) (map[string]bool, error) {
	rows, err := s.pool.Query(ctx, `SELECT address FROM proxies`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]bool{}
	for rows.Next() {
		var address string
		if err := rows.Scan(&address); err != nil {
			continue
		}
		out[address] = true
	}
	return out, rows.Err()
}

// addDiscovered inserts newly-found public proxies, skipping any address already present
// (see DiscoveredEntry doc) — returns how many were actually new.
func (s *Store) addDiscovered(ctx context.Context, entries []DiscoveredEntry) (added int, err error) {
	for _, e := range entries {
		tag, execErr := s.pool.Exec(ctx, `
			INSERT INTO proxies (address, protocol, country_code, source)
			VALUES ($1, $2, NULLIF($3, ''), $4)
			ON CONFLICT (address) DO NOTHING
		`, e.Address, e.Protocol, e.CountryCode, e.Source)
		if execErr != nil {
			return added, fmt.Errorf("add discovered proxy %s: %w", e.Address, execErr)
		}
		if tag.RowsAffected() > 0 {
			added++
		}
	}
	return added, nil
}

// checkOneProxyAlive dispatches to the right aliveness check for a proxy's protocol —
// shared by the hourly sweep (checkAllAliveness) and the admin dashboard's single-proxy
// "ping" action (Store.CheckOne).
func checkOneProxyAlive(ctx context.Context, protocol, address, username, password string) bool {
	if protocol == "http" || protocol == "https" {
		return CheckAliveHTTP(ctx, address, username, password)
	}
	var auth *xproxy.Auth
	if username != "" {
		auth = &xproxy.Auth{User: username, Password: password}
	}
	return CheckAlive(ctx, address, auth)
}

// CheckOne re-checks one proxy's aliveness immediately (rather than waiting for the next
// hourly sweep — see Refresh) and persists the result, for the admin dashboard's
// per-proxy "ping" action. Returns its address (for the caller's response) and whether it
// answered alive.
func (s *Store) CheckOne(ctx context.Context, id string) (address string, alive bool, err error) {
	var protocol string
	var username, password *string
	err = s.pool.QueryRow(ctx, `SELECT address, protocol, username, password FROM proxies WHERE id = $1`, id).
		Scan(&address, &protocol, &username, &password)
	if err != nil {
		return "", false, err
	}

	u, p := "", ""
	if username != nil {
		u = *username
	}
	if password != nil {
		p = *password
	}

	checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	alive = checkOneProxyAlive(checkCtx, protocol, address, u, p)
	cancel()

	status := "dead"
	if alive {
		status = "alive"
	}
	if _, execErr := s.pool.Exec(ctx, `
		UPDATE proxies SET status = $2, last_checked_at = now(), updated_at = now() WHERE id = $1
	`, id, status); execErr != nil {
		return address, alive, execErr
	}
	return address, alive, nil
}

func (s *Store) checkAllAliveness(ctx context.Context, log zerolog.Logger) {
	checked, alive, err := s.recheckProxies(ctx, `SELECT id, address, protocol, username, password FROM proxies`)
	if err != nil {
		log.Error().Err(err).Msg("proxypool: failed to list proxies for aliveness check")
		return
	}
	log.Info().Int("alive", alive).Int("dead", checked-alive).Msg("proxypool: aliveness check complete")
}

// RecheckDeadAndUnknown re-checks every proxy not currently marked 'alive' (i.e. 'dead',
// or 'unknown' because it's never been checked yet) right now, instead of waiting for the
// next hourly sweep (see Refresh) — the admin dashboard's "Recheck dead & unknown" bulk
// action. Already-alive proxies are skipped: re-confirming those doesn't tell us anything
// we don't already believe.
func (s *Store) RecheckDeadAndUnknown(ctx context.Context) (checked, alive int, err error) {
	return s.recheckProxies(ctx, `
		SELECT id, address, protocol, username, password FROM proxies WHERE status != 'alive'
	`)
}

// recheckProxies runs the concurrent aliveness check (see checkOneProxyAlive) against
// whatever set of proxies query selects (must return id, address, protocol, username,
// password, in that order) and persists each result — shared by the hourly sweep
// (checkAllAliveness) and the admin dashboard's on-demand bulk recheck
// (RecheckDeadAndUnknown), which only differ in which proxies they target.
func (s *Store) recheckProxies(ctx context.Context, query string, args ...any) (checked, alive int, err error) {
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return 0, 0, err
	}
	type entry struct {
		id, address, protocol string
		username, password    *string
	}
	var all []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.id, &e.address, &e.protocol, &e.username, &e.password); err != nil {
			continue
		}
		all = append(all, e)
	}
	rows.Close()

	sem := make(chan struct{}, maxAliveCheckConcurrency)
	var wg sync.WaitGroup
	var aliveCount int
	var mu sync.Mutex

	for _, e := range all {
		wg.Add(1)
		sem <- struct{}{}
		go func(e entry) {
			defer wg.Done()
			defer func() { <-sem }()

			username, password := "", ""
			if e.username != nil {
				username = *e.username
			}
			if e.password != nil {
				password = *e.password
			}

			checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			alive := checkOneProxyAlive(checkCtx, e.protocol, e.address, username, password)
			cancel()

			status := "dead"
			if alive {
				status = "alive"
			}
			mu.Lock()
			if alive {
				aliveCount++
			}
			mu.Unlock()

			s.pool.Exec(ctx, `
				UPDATE proxies SET status = $2, last_checked_at = now(), updated_at = now()
				WHERE id = $1
			`, e.id, status)
		}(e)
	}
	wg.Wait()

	return len(all), aliveCount, nil
}

// Pick returns a currently-alive proxy, chosen at random, and records the pick
// (use_count, last_used_at) so the admin dashboard can show which proxies are actually
// carrying traffic. Returns ok=false if the pool has no alive proxy right now.
func (s *Store) Pick(ctx context.Context) (picked PickedProxy, ok bool) {
	return s.pickByCountry(ctx, "")
}

// PickForURL applies locale routing before falling back to the whole pool. Russian and
// Belarusian storefronts are requested through RU exits because they commonly localize
// catalogues and availability by visitor country.
func (s *Store) PickForURL(ctx context.Context, rawURL string) (picked PickedProxy, ok bool) {
	if RequiresRussianProxy(rawURL) {
		if picked, ok := s.pickByCountry(ctx, "RU"); ok {
			return picked, true
		}
	}
	return s.pickByCountry(ctx, "")
}

// PickNonResidential returns a free/datacenter/unknown proxy for the cheap retry tier.
func (s *Store) PickNonResidential(ctx context.Context) (PickedProxy, bool) {
	return s.pickByTier(ctx, "non_residential", "")
}

// PickResidentialForURL is the expensive anti-bot tier. DataImpulse country targeting
// and a stable per-domain session are encoded in the username at request time, so one
// stored gateway can serve every market without duplicate database rows.
func (s *Store) PickResidentialForURL(ctx context.Context, rawURL string) (PickedProxy, bool) {
	return s.pickByTier(ctx, "residential", rawURL)
}

func (s *Store) pickByTier(ctx context.Context, tier, rawURL string) (picked PickedProxy, ok bool) {
	var username, password, provider *string
	err := s.pool.QueryRow(ctx, `
		UPDATE proxies SET use_count = use_count + 1, last_used_at = now(), updated_at = now()
		WHERE id = (
			SELECT id FROM proxies
			WHERE status = 'alive'
			  AND (($1 = 'residential' AND network_type = 'residential')
			    OR ($1 = 'non_residential' AND network_type != 'residential'))
			ORDER BY random() LIMIT 1
		)
		RETURNING address, username, password, provider
	`, tier).Scan(&picked.Address, &username, &password, &provider)
	if err != nil {
		return PickedProxy{}, false
	}
	if username != nil {
		picked.Username = *username
	}
	if password != nil {
		picked.Password = *password
	}
	if provider != nil && strings.EqualFold(*provider, "DataImpulse") && picked.Username != "" {
		picked.Username = dataImpulseUsername(picked.Username, rawURL)
	}
	return picked, true
}

func dataImpulseUsername(base, rawURL string) string {
	parsed, _ := url.Parse(rawURL)
	host := strings.ToLower(parsed.Hostname())
	country := ""
	switch {
	case strings.HasSuffix(host, ".by"):
		country = "by"
	case strings.HasSuffix(host, ".ru"):
		country = "ru"
	case strings.HasSuffix(host, ".pl"):
		country = "pl"
	}
	if strings.Contains(base, "__") {
		base = strings.SplitN(base, "__", 2)[0]
	}
	params := ""
	if country != "" {
		params = "__cr." + country
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(host))
	return fmt.Sprintf("%s%s;sessid.%x", base, params, h.Sum32())
}

func (s *Store) pickByCountry(ctx context.Context, country string) (picked PickedProxy, ok bool) {
	var username, password *string
	err := s.pool.QueryRow(ctx, `
		UPDATE proxies SET use_count = use_count + 1, last_used_at = now(), updated_at = now()
		WHERE id = (
			SELECT id FROM proxies
			WHERE status = 'alive' AND ($1 = '' OR country_code = $1)
			ORDER BY random() LIMIT 1
		)
		RETURNING address, username, password
	`, country).Scan(&picked.Address, &username, &password)
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

// RequiresRussianProxy reports whether a hostname belongs to the Russian or Belarusian
// country-code namespace. URL parsing avoids false positives such as example.ru.com.
func RequiresRussianProxy(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	return strings.HasSuffix(host, ".ru") || strings.HasSuffix(host, ".by") || host == "ru" || host == "by"
}

// FetchFingerprint is a (User-Agent, proxy) pairing that previously produced a real page
// (not a bot-detection block) for one specific URL — see GetFingerprint/SaveFingerprint.
// A zero-value Proxy (Address == "") means the fetch went out directly, no proxy.
type FetchFingerprint struct {
	UserAgent string
	Proxy     PickedProxy
}

// GetFingerprint returns the last combination that successfully fetched this exact URL,
// if any — so a repeat scrape (e.g. the next scheduled price check) can retry what's
// already known to work before gambling on a fresh random pick. If the saved proxy has
// since been marked dead by the hourly aliveness check, this returns ok=false rather than
// handing back a pairing already known not to work — the caller falls straight through to
// a fresh pick instead of wasting a request (and its timeout) on a proxy we already know
// is gone, even though a saved fingerprint exists for this URL.
func (s *Store) GetFingerprint(ctx context.Context, url string) (FetchFingerprint, bool) {
	var fp FetchFingerprint
	var proxyAddress, proxyUsername, proxyPassword *string
	err := s.pool.QueryRow(ctx, `
		SELECT sf.user_agent, sf.proxy_address, sf.proxy_username, sf.proxy_password
		FROM scrape_fingerprints sf
		LEFT JOIN proxies p ON p.address = sf.proxy_address
		WHERE sf.url = $1 AND (sf.proxy_address IS NULL OR p.status = 'alive')
	`, url).Scan(&fp.UserAgent, &proxyAddress, &proxyUsername, &proxyPassword)
	if err != nil {
		return FetchFingerprint{}, false
	}
	if proxyAddress != nil {
		fp.Proxy.Address = *proxyAddress
	}
	if proxyUsername != nil {
		fp.Proxy.Username = *proxyUsername
	}
	if proxyPassword != nil {
		fp.Proxy.Password = *proxyPassword
	}
	return fp, true
}

// SaveFingerprint remembers that this (User-Agent, proxy) pairing just fetched url
// successfully, overwriting whatever was saved before — the most recent success is the
// best bet for next time, not necessarily the first one ever found.
func (s *Store) SaveFingerprint(ctx context.Context, url string, fp FetchFingerprint) error {
	var proxyAddress, proxyUsername, proxyPassword *string
	if fp.Proxy.Address != "" {
		proxyAddress = &fp.Proxy.Address
		if fp.Proxy.Username != "" {
			proxyUsername = &fp.Proxy.Username
			proxyPassword = &fp.Proxy.Password
		}
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO scrape_fingerprints (url, user_agent, proxy_address, proxy_username, proxy_password, success_count, last_used_at)
		VALUES ($1, $2, $3, $4, $5, 1, now())
		ON CONFLICT (url) DO UPDATE SET
			user_agent = $2,
			proxy_address = $3,
			proxy_username = $4,
			proxy_password = $5,
			success_count = scrape_fingerprints.success_count + 1,
			last_used_at = now(),
			updated_at = now()
	`, url, fp.UserAgent, proxyAddress, proxyUsername, proxyPassword)
	return err
}

// DeleteDead removes every proxy currently marked 'dead' (see checkAllAliveness) from the
// pool — for the admin dashboard's "delete dead proxies" action, so a pool that's
// accumulated a lot of no-longer-usable public/free entries (see FetchGeonode) can be
// pruned by hand instead of waiting for a source to naturally stop listing them. 'unknown'
// (never yet checked) proxies are left alone — they haven't failed anything yet.
func (s *Store) DeleteDead(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM proxies WHERE status = 'dead'`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// List returns every proxy in the pool for the admin dashboard, most recently used first.
// Never returns a password — HasAuth just indicates whether the proxy is authenticated.
func (s *Store) List(ctx context.Context) ([]Proxy, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, address, protocol, COALESCE(country_code, ''), status, source,
		       network_type, COALESCE(provider, ''), COALESCE(asn, ''),
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
			&p.NetworkType, &p.Provider, &p.ASN,
			&p.HasAuth, &p.UseCount, &p.LastCheckedAt, &p.LastUsedAt, &p.CreatedAt); err != nil {
			return nil, err
		}
		proxies = append(proxies, p)
	}
	return proxies, rows.Err()
}
