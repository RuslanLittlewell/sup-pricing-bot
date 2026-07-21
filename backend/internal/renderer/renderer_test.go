package renderer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
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
