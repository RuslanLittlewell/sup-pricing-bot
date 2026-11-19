package renderer

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/littlewell/price-tracker/internal/proxypool"
)

func TestExtensionStarted(t *testing.T) {
	profile := t.TempDir()
	defaultDir := filepath.Join(profile, "Default")
	if err := os.MkdirAll(defaultDir, 0700); err != nil {
		t.Fatal(err)
	}
	prefs := `{"extensions":{"settings":{"test":{"path":"/opt/extensions/antibot-detector","disable_reasons":[],"has_started_service_worker":true}}}}`
	if err := os.WriteFile(filepath.Join(defaultDir, "Preferences"), []byte(prefs), 0600); err != nil {
		t.Fatal(err)
	}
	if !extensionStarted(profile, "/opt/extensions/antibot-detector") {
		t.Fatal("expected registered, enabled service worker to be detected")
	}
	if extensionStarted(profile, "/opt/extensions/other") {
		t.Fatal("unexpected match for another extension path")
	}
}

func TestEvictSessionRemovesOnlyExpectedBrowser(t *testing.T) {
	oldCtx, oldCancel := context.WithCancel(context.Background())
	newCtx := context.Background()
	r := &Renderer{sessions: map[string]*browserSession{
		"example.com": {ctx: oldCtx, cancel: oldCancel, lastUsed: time.Now()},
	}}

	r.evictSession("https://example.com/item", newCtx)
	if r.sessions["example.com"] == nil {
		t.Fatal("a replacement browser must not be evicted by an older render")
	}

	r.evictSession("https://example.com/item", oldCtx)
	if r.sessions["example.com"] != nil {
		t.Fatal("expected canceled browser session to be evicted")
	}
}

func TestUsesLegacyRendererOnlyForZara(t *testing.T) {
	for _, host := range []string{"zara.com", "www.zara.com", "WWW.ZARA.COM."} {
		if !usesLegacyRenderer(host) {
			t.Fatalf("expected Zara legacy renderer for %q", host)
		}
	}
	for _, host := range []string{"notzara.com", "zara.com.example.org", "nike.com"} {
		if usesLegacyRenderer(host) {
			t.Fatalf("unexpected Zara legacy renderer for %q", host)
		}
	}
}

func TestPrepareIsNoOpOncePersistentSessionIsPrepared(t *testing.T) {
	// A prepared session must not touch CDP again: re-running setup on a reused tab is
	// what used to stack listeners and swap the identity mid-session. The context here
	// has no browser at all, so any CDP call would fail.
	r := &Renderer{}
	session := &browserSession{ctx: context.Background(), prepared: true, proxyUser: "user"}
	if err := r.prepare(session).Do(context.Background()); err != nil {
		t.Fatalf("prepared session must skip setup, got %v", err)
	}
}

func TestCloseIdleSessionsKeepsActiveAndRequestedHost(t *testing.T) {
	idleCtx, idleCancel := context.WithCancel(context.Background())
	keepCtx, keepCancel := context.WithCancel(context.Background())
	activeCtx, activeCancel := context.WithCancel(context.Background())
	defer keepCancel()
	defer activeCancel()
	stale := time.Now().Add(-2 * sessionIdleTimeout)
	r := &Renderer{sessions: map[string]*browserSession{
		"idle.example":   {ctx: idleCtx, cancel: idleCancel, lastUsed: stale},
		"keep.example":   {ctx: keepCtx, cancel: keepCancel, lastUsed: stale},
		"active.example": {ctx: activeCtx, cancel: activeCancel, lastUsed: time.Now()},
	}}

	r.closeIdleSessionsLocked("keep.example")

	if r.sessions["idle.example"] != nil || idleCtx.Err() == nil {
		t.Fatal("an idle browser must be closed")
	}
	if r.sessions["keep.example"] == nil || keepCtx.Err() != nil {
		t.Fatal("the host about to be rendered must not be closed")
	}
	if r.sessions["active.example"] == nil {
		t.Fatal("a recently used browser must stay warm")
	}
}

func TestParseBrowserVersion(t *testing.T) {
	cases := []struct {
		out, version, product string
	}{
		{"Chromium 148.0.7778.96 Alpine Linux\n", "148.0.7778.96", ""},
		{"Google Chrome 148.0.7778.97 \n", "148.0.7778.97", "Google Chrome"},
	}
	for _, c := range cases {
		version, product, ok := parseBrowserVersion(c.out)
		if !ok || version != c.version || product != c.product {
			t.Errorf("parseBrowserVersion(%q) = %q, %q, %v", c.out, version, product, ok)
		}
	}
	if _, _, ok := parseBrowserVersion("command not found"); ok {
		t.Fatal("output without a version must be rejected")
	}
}

func TestTimezoneFollowsExitCountry(t *testing.T) {
	cases := map[string]string{
		"www.wildberries.ru": "Europe/Moscow",
		"shop.example.by":    "Europe/Minsk",
		"allegro.pl":         "Europe/Warsaw",
		"www.zara.com":       "Europe/Warsaw", // exits default to PL, see proxypool
	}
	for host, want := range cases {
		if got := timezoneFor(host); got != want {
			t.Errorf("timezoneFor(%q) = %q, want %q", host, got, want)
		}
	}
}

type fakeProxySource struct{}

func (fakeProxySource) PickResidentialForURL(context.Context, string) (proxypool.PickedProxy, bool) {
	return proxypool.PickedProxy{}, false
}

func TestRotationRelaunchesBrowserOnNewExit(t *testing.T) {
	const page = "https://www.renderer-rotation.example/p/1"
	session := &browserSession{proxyGen: proxypool.ResidentialGeneration(page)}

	if (&Renderer{}).rotatedSince(session, page) {
		t.Fatal("without a proxy pool there is no exit to rotate")
	}
	r := &Renderer{proxies: fakeProxySource{}}
	if r.rotatedSince(session, page) {
		t.Fatal("a browser on the host's current exit must be reused")
	}
	proxypool.NewStore(nil).RotateResidentialSession(page)
	if !r.rotatedSince(session, page) {
		t.Fatal("after rotation the browser still holds the burned exit and must relaunch")
	}
}

func TestPruneProfilesRemovesOnlyIdleUntrackedHosts(t *testing.T) {
	root := t.TempDir()
	old := time.Now().Add(-3 * ProfilePruneInterval)
	mkProfile := func(rel string, modTime time.Time) string {
		dir := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Join(dir, "Default"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "Default", "Cookies"), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(dir, modTime, modTime); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	gone := mkProfile("gone.example", old)
	tracked := mkProfile("tracked.example", old)
	recent := mkProfile("recent.example", time.Now())
	warm := mkProfile("warm.example", old)
	busy := mkProfile("busy.example", old)
	legacyGone := mkProfile(filepath.Join("legacy", "gone.zara.example"), old)
	legacyTracked := mkProfile(filepath.Join("legacy", "www.zara.com"), old)
	// The legacy container itself is old and untracked, but it is not a host profile.
	if err := os.Chtimes(filepath.Join(root, "legacy"), old, old); err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(stray, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}

	warmCtx, warmCancel := context.WithCancel(context.Background())
	defer warmCancel()
	busyLock := &sync.Mutex{}
	busyLock.Lock() // a render in progress holds the host lock
	defer busyLock.Unlock()
	r := &Renderer{
		profileRoot:  root,
		profileLocks: map[string]*sync.Mutex{"busy.example": busyLock},
		sessions:     map[string]*browserSession{"warm.example": {ctx: warmCtx, cancel: warmCancel}},
	}
	trackedKey, _ := ProfileKey("https://tracked.example/p/1?x=2")
	zaraKey, _ := ProfileKey("https://www.zara.com/pl/en/x.html")

	removed, err := r.PruneProfiles(map[string]bool{trackedKey: true, zaraKey: true}, ProfilePruneInterval)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]bool{"gone.example": true, filepath.Join("legacy", "gone.zara.example"): true}
	if len(removed) != len(want) {
		t.Fatalf("removed %v, want %v", removed, want)
	}
	for _, name := range removed {
		if !want[name] {
			t.Fatalf("removed %v, want %v", removed, want)
		}
	}
	for _, dir := range []string{gone, legacyGone} {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatalf("%s should have been deleted", dir)
		}
	}
	for _, path := range []string{tracked, recent, warm, busy, legacyTracked, stray} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s must be kept: %v", path, err)
		}
	}
}

func TestPruneProfilesWithoutLegacyDir(t *testing.T) {
	r := &Renderer{profileRoot: t.TempDir(), profileLocks: map[string]*sync.Mutex{}, sessions: map[string]*browserSession{}}
	if removed, err := r.PruneProfiles(nil, time.Hour); err != nil || len(removed) != 0 {
		t.Fatalf("removed=%v err=%v", removed, err)
	}
}
