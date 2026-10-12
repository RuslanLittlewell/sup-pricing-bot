package extractor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/littlewell/price-tracker/internal/proxypool"
)

type fakePool struct {
	mu          sync.Mutex
	saved       *proxypool.FetchFingerprint
	savedNow    *proxypool.FetchFingerprint
	nonRes      *proxypool.PickedProxy
	residential *proxypool.PickedProxy
	rotations   int
}

func (p *fakePool) GetFingerprint(context.Context, string) (proxypool.FetchFingerprint, bool) {
	if p.saved == nil {
		return proxypool.FetchFingerprint{}, false
	}
	return *p.saved, true
}

func (p *fakePool) SaveFingerprint(_ context.Context, _ string, fp proxypool.FetchFingerprint) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.savedNow = &fp
	return nil
}

func (p *fakePool) PickNonResidential(context.Context) (proxypool.PickedProxy, bool) {
	if p.nonRes == nil {
		return proxypool.PickedProxy{}, false
	}
	return *p.nonRes, true
}

func (p *fakePool) PickResidentialForURL(context.Context, string) (proxypool.PickedProxy, bool) {
	if p.residential == nil {
		return proxypool.PickedProxy{}, false
	}
	return *p.residential, true
}

func (p *fakePool) RotateResidentialSession(string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rotations++
}

// fakeCurlRelay answers like curl-cffi-relay/server.py, choosing the outcome per exit
// ("" = direct) and recording which exits were asked for, in order.
type fakeCurlRelay struct {
	mu       sync.Mutex
	outcomes map[string]string // proxy -> "200", "403", "404", "502"
	asked    []string
}

func (r *fakeCurlRelay) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	var payload curlCFFIRequest
	_ = json.NewDecoder(req.Body).Decode(&payload)
	r.mu.Lock()
	r.asked = append(r.asked, payload.Proxy)
	r.mu.Unlock()
	switch outcome := r.outcomes[payload.Proxy]; outcome {
	case "502":
		http.Error(w, "upstream fetch failed", http.StatusBadGateway)
	case "200":
		w.Header().Set("X-Upstream-Status", "200")
		fmt.Fprintf(w, "<html>product via %q</html>", payload.Proxy)
	default:
		w.Header().Set("X-Upstream-Status", outcome)
		fmt.Fprint(w, "<html>denied</html>")
	}
}

func newChainFetcher(t *testing.T, relay http.Handler, pool proxyPool) *PageFetcher {
	t.Helper()
	srv := httptest.NewServer(relay)
	t.Cleanup(srv.Close)
	return &PageFetcher{
		curlCFFI: &CurlCFFIFetcher{baseURL: srv.URL, client: &http.Client{Timeout: 5 * time.Second}},
		proxies:  pool,
	}
}

const (
	publicSocks = "socks5h://1.2.3.4:1080"
	paidExit    = "http://u:p@gw.example:823"
)

func chainPool() *fakePool {
	return &fakePool{
		nonRes:      &proxypool.PickedProxy{Address: "1.2.3.4:1080", Protocol: "socks5"},
		residential: &proxypool.PickedProxy{Address: "gw.example:823", Username: "u", Password: "p", Protocol: "socks5"},
	}
}

func TestFetchHTTPEscalatesFromFreeToPaidExits(t *testing.T) {
	relay := &fakeCurlRelay{outcomes: map[string]string{"": "403", publicSocks: "502", paidExit: "200"}}
	pool := chainPool()
	f := newChainFetcher(t, relay, pool)

	body, method, err := f.fetchHTTP("https://shop.example/p/1")
	if err != nil {
		t.Fatal(err)
	}
	if method != FetchMethodCurlCFFI || !strings.Contains(string(body), "gw.example") {
		t.Fatalf("method=%s body=%s", method, body)
	}
	if got, want := strings.Join(relay.asked, " | "), " | "+publicSocks+" | "+paidExit; got != want {
		t.Fatalf("exits tried = %q, want %q (server IP, public socks5 as socks5h, then paid)", got, want)
	}
	if pool.savedNow == nil || pool.savedNow.Proxy.Address != "gw.example:823" || pool.savedNow.UserAgent != curlCFFIFingerprint {
		t.Fatalf("the winning exit must be remembered for next time: %+v", pool.savedNow)
	}
	if pool.rotations != 0 {
		t.Fatal("a residential exit that worked must not be rotated away")
	}
}

func TestFetchHTTPStartsFromTheExitThatLastWorked(t *testing.T) {
	relay := &fakeCurlRelay{outcomes: map[string]string{paidExit: "200"}}
	pool := chainPool()
	pool.saved = &proxypool.FetchFingerprint{UserAgent: curlCFFIFingerprint, Proxy: *pool.residential}
	f := newChainFetcher(t, relay, pool)

	if _, _, err := f.fetchHTTP("https://shop.example/p/1"); err != nil {
		t.Fatal(err)
	}
	if len(relay.asked) != 1 || relay.asked[0] != paidExit {
		t.Fatalf("exits known to fail must be skipped, asked %q", relay.asked)
	}
}

func TestFetchHTTPStopsOnDefinitiveStatus(t *testing.T) {
	relay := &fakeCurlRelay{outcomes: map[string]string{"": "404"}}
	f := newChainFetcher(t, relay, chainPool())

	_, _, err := f.fetchHTTP("https://shop.example/gone")
	if err == nil || shouldRenderFallback(err) || errors.Is(err, errBotBlocked) {
		t.Fatalf("a 404 is final, got %v", err)
	}
	if len(relay.asked) != 1 {
		t.Fatalf("no other exit can change a 404, but asked %q", relay.asked)
	}
}

func TestFetchHTTPRotatesBlockedResidentialExit(t *testing.T) {
	relay := &fakeCurlRelay{outcomes: map[string]string{"": "403", publicSocks: "403", paidExit: "403"}}
	pool := chainPool()
	f := newChainFetcher(t, relay, pool)

	if _, _, err := f.fetchHTTP("https://shop.example/p/1"); err == nil || !shouldRenderFallback(err) {
		t.Fatalf("an all-exits block must stay retryable for the browser tiers, got %v", err)
	}
	if pool.rotations != 1 {
		t.Fatalf("the refused residential exit must be rotated once, got %d", pool.rotations)
	}
}

func TestFetchHTTPFallsBackToUTLSWhenRelayIsDown(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close() // nothing listens here any more
	f := &PageFetcher{curlCFFI: &CurlCFFIFetcher{baseURL: srv.URL, client: &http.Client{Timeout: time.Second}}}

	_, err := f.fetchViaCurlCFFI("https://shop.example/p/1", "", time.Second)
	if !errors.Is(err, errRelayUnavailable) {
		t.Fatalf("an unreachable relay must be told apart from a refused target, got %v", err)
	}
}

func TestCurlCFFIErrorsDoNotLeakProxyCredentials(t *testing.T) {
	relay := &fakeCurlRelay{outcomes: map[string]string{paidExit: "502"}}
	f := newChainFetcher(t, relay, nil)

	_, err := f.fetchViaCurlCFFI("https://shop.example/p/1", paidExit, time.Second)
	if err == nil || !shouldRenderFallback(err) {
		t.Fatalf("a failed exit must let the chain move on, got %v", err)
	}
	if strings.Contains(err.Error(), "u:p@") {
		t.Fatalf("error message leaks proxy credentials: %v", err)
	}
}

func TestFirefoxRejectsUpstreamErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Upstream-Status", "403")
		fmt.Fprint(w, "<html>plain deny page without known markers</html>")
	}))
	defer srv.Close()
	ff := &FirefoxFetcher{baseURL: srv.URL, client: srv.Client()}

	if _, err := ff.Fetch("https://www2.hm.com/pl_pl/productpage.1.html", "", nil); err == nil {
		t.Fatal("a 403 rendered by Camoufox must not pass as the product page")
	}
}
