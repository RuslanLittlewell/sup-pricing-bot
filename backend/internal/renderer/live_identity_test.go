//go:build livechrome

// Drives a real Chrome against a local page, through a local authenticating proxy, and
// checks what a site can observe of the renderer — the part unit tests can't see (CDP
// semantics, header generation, browser lifetime, proxy auth). Opt-in because it needs a
// browser:
//
//	CHROME_PATH="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" \
//	  go test -tags livechrome -run TestLiveRenderIdentity -v ./internal/renderer/
package renderer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/littlewell/price-tracker/internal/proxypool"
)

const livePage = `<!doctype html><html><head><title>t</title></head><body>
<img src="/img.png"><pre id="out">pending</pre>
<script>
(async () => {
  const out = {};
  out.ua = navigator.userAgent;
  out.platform = navigator.platform;
  out.webdriver = navigator.webdriver;
  out.languages = navigator.languages;
  out.ownProps = Object.getOwnPropertyNames(navigator);
  out.timezone = Intl.DateTimeFormat().resolvedOptions().timeZone;
  out.uad = navigator.userAgentData ? {brands: navigator.userAgentData.brands, platform: navigator.userAgentData.platform} : null;
  if (navigator.userAgentData) out.he = await navigator.userAgentData.getHighEntropyValues(["fullVersionList","platformVersion","architecture","formFactors"]);
  try { await fetch("/api"); } catch (e) { out.fetchErr = String(e); }
  out.workerUA = await new Promise((res) => {
    const w = new Worker(URL.createObjectURL(new Blob(["postMessage(navigator.userAgent)"], {type: "text/javascript"})));
    w.onmessage = (e) => res(e.data);
    setTimeout(() => res("timeout"), 1500);
  });
  document.getElementById("out").textContent = JSON.stringify(out);
})();
</script></body></html>`

// authProxy is a plain-HTTP forward proxy that demands Basic credentials, like the
// residential gateway, and stamps what it forwarded so the page server can tell.
type authProxy struct {
	user, pass string
	forwarded  atomic.Int32
	challenged atomic.Int32
}

func (p *authProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte(p.user+":"+p.pass))
	if r.Header.Get("Proxy-Authorization") != want {
		p.challenged.Add(1)
		w.Header().Set("Proxy-Authenticate", `Basic realm="test"`)
		w.WriteHeader(http.StatusProxyAuthRequired)
		return
	}
	out, _ := http.NewRequest(r.Method, r.URL.String(), r.Body)
	out.Header = r.Header.Clone()
	out.Header.Del("Proxy-Authorization")
	out.Header.Set("X-Test-Proxy", "1")
	resp, err := http.DefaultTransport.RoundTrip(out)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	p.forwarded.Add(1)
	for k, v := range resp.Header {
		w.Header()[k] = v
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

type countingSource struct {
	address string
	picks   atomic.Int32
}

func (s *countingSource) PickResidentialForURL(context.Context, string) (proxypool.PickedProxy, bool) {
	s.picks.Add(1)
	return proxypool.PickedProxy{Address: s.address, Username: "u", Password: "p"}, true
}

func TestLiveRenderIdentity(t *testing.T) {
	execPath := os.Getenv("CHROME_PATH")
	if execPath == "" {
		t.Skip("set CHROME_PATH")
	}

	var mu sync.Mutex
	headers := map[string]http.Header{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		headers[req.URL.Path] = req.Header.Clone()
		mu.Unlock()
		switch req.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, livePage)
		case "/api":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	proxy := &authProxy{user: "u", pass: "p"}
	proxySrv := httptest.NewServer(proxy)
	defer proxySrv.Close()
	proxyURL, _ := url.Parse(proxySrv.URL)
	source := &countingSource{address: proxyURL.Host}

	identity := detectIdentity(execPath)
	t.Logf("detected identity: %s (full %s, brands %v)", identity.UserAgent, identity.FullVersion, identity.Brands)
	newRenderer := func(root string) *Renderer {
		rootCtx, cancel := context.WithCancel(context.Background())
		r := &Renderer{
			rootCtx: rootCtx, cancel: cancel, profileRoot: root, profileLocks: map[string]*sync.Mutex{},
			persistent: true, maxSessions: 4, sessions: map[string]*browserSession{},
			execPath: execPath, identity: identity,
			// Chrome never proxies loopback unless told to; the real gateway isn't loopback.
			extraFlags: []chromedp.ExecAllocatorOption{chromedp.Flag("proxy-bypass-list", "<-loopback>")},
		}
		r.UseProxyPool(source)
		return r
	}
	r := newRenderer(t.TempDir())
	defer r.Close()

	outRe := regexp.MustCompile(`(?s)<pre id="out">(.*?)</pre>`)
	read := func(path string) map[string]any {
		start := time.Now()
		got, err := r.Render(nil, srv.URL+path, 3*time.Second)
		if err != nil {
			t.Fatalf("render %s: %v (after %s)", path, err, time.Since(start))
		}
		m := outRe.FindStringSubmatch(got)
		if m == nil {
			t.Fatalf("no output in %s", got)
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(html.UnescapeString(m[1])), &out); err != nil {
			t.Fatalf("decode %q: %v", m[1], err)
		}
		t.Logf("render %s took %s", path, time.Since(start))
		return out
	}

	first := read("/")
	mu.Lock()
	doc, img, api := headers["/"], headers["/img.png"], headers["/api"]
	mu.Unlock()
	pretty, _ := json.MarshalIndent(first, "", "  ")
	t.Logf("first render:\n%s", pretty)
	t.Logf("document headers: %v", doc)
	t.Logf("api headers: %v", api)

	// Proxy: authenticated by the CDP handler, and actually used.
	if proxy.challenged.Load() == 0 || doc.Get("X-Test-Proxy") != "1" {
		t.Errorf("the page must arrive through the authenticated proxy (challenged=%d, doc=%v)", proxy.challenged.Load(), doc)
	}
	// Render on a residential exit skips images.
	if img != nil {
		t.Errorf("an image was fetched through the billed exit: %v", img)
	}
	// Identity: the real browser, consistently everywhere.
	if first["ua"] != identity.UserAgent || first["workerUA"] != identity.UserAgent {
		t.Errorf("UA page=%v worker=%v, want %s", first["ua"], first["workerUA"], identity.UserAgent)
	}
	if first["platform"] != identity.NavigatorPlatform {
		t.Errorf("navigator.platform = %v, want %s", first["platform"], identity.NavigatorPlatform)
	}
	if doc.Get("Sec-Ch-Ua") != identity.SecCHUA {
		t.Errorf("Sec-CH-UA = %q, want %q", doc.Get("Sec-Ch-Ua"), identity.SecCHUA)
	}
	if he, _ := first["he"].(map[string]any); he == nil || !strings.Contains(fmt.Sprint(he["fullVersionList"]), identity.FullVersion) {
		t.Errorf("high-entropy fullVersionList must carry the real build %s: %v", identity.FullVersion, first["he"])
	}
	if first["webdriver"] != false {
		t.Errorf("navigator.webdriver = %v, want false like real Chrome", first["webdriver"])
	}
	if props, _ := first["ownProps"].([]any); len(props) != 0 {
		t.Errorf("navigator has own (patched) properties: %v", props)
	}
	if first["timezone"] != "Europe/Warsaw" {
		t.Errorf("timezone = %v, want Europe/Warsaw to match the PL exit", first["timezone"])
	}
	if got := doc.Get("Accept-Language"); got != "pl-PL,pl;q=0.9,en-US;q=0.8,en;q=0.7" {
		t.Errorf("Accept-Language = %q", got)
	}

	// Same exit, same browser: no relaunch.
	second := read("/?again=1")
	if source.picks.Load() != 1 || second["ua"] != first["ua"] {
		t.Errorf("a persistent session must be reused (picks=%d)", source.picks.Load())
	}

	// After a rotation the browser holds a burned exit and must come back on a new one.
	proxypool.NewStore(nil).RotateResidentialSession(srv.URL)
	read("/?rotated=1")
	if source.picks.Load() != 2 {
		t.Errorf("rotation must relaunch the browser on a freshly picked exit (picks=%d)", source.picks.Load())
	}

	// FindPriceBlock keeps images: its screenshot is shown to the user.
	mu.Lock()
	delete(headers, "/img.png")
	mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, _, _ = r.FindPriceBlock(ctx, srv.URL+"/?screenshot=1", "1", 0)
	mu.Lock()
	gotImg := headers["/img.png"] != nil
	mu.Unlock()
	if !gotImg {
		t.Error("media skipping leaked out of Render into FindPriceBlock")
	}
}
