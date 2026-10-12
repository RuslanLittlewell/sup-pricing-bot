package useragent

import (
	"fmt"
	"strings"
	"testing"
)

func TestProfilesKeepHeaderAndMetadataInSync(t *testing.T) {
	for _, p := range profiles {
		quoted := make([]string, len(p.Brands))
		for i, b := range p.Brands {
			quoted[i] = fmt.Sprintf(`"%s";v="%s"`, b.Name, b.Version)
		}
		if got := strings.Join(quoted, ", "); got != p.SecCHUA {
			t.Errorf("Sec-CH-UA %q disagrees with metadata brands %q", p.SecCHUA, got)
		}
		if major, _, _ := strings.Cut(p.FullVersion, "."); !strings.Contains(p.UserAgent, "Chrome/"+major+".0.0.0 ") {
			t.Errorf("UA %q does not carry the reduced form of %q", p.UserAgent, p.FullVersion)
		}
		if `"`+p.CDPPlatform+`"` != p.SecCHUAPlatform {
			t.Errorf("client-hint platform %q disagrees with %q", p.CDPPlatform, p.SecCHUAPlatform)
		}
		if (p.CDPPlatform == "Linux") != (p.CHPlatformVersion == "") {
			t.Errorf("profile %q: platform version %q (Chromium reports it empty on Linux only)", p.UserAgent, p.CHPlatformVersion)
		}
	}
}

func TestBrandListMatchesRealChrome(t *testing.T) {
	// Sec-CH-UA as sent by real Chrome releases.
	cases := map[int]string{
		120: `"Not_A Brand";v="8", "Chromium";v="120", "Google Chrome";v="120"`,
		124: `"Chromium";v="124", "Google Chrome";v="124", "Not-A.Brand";v="99"`,
		131: `"Google Chrome";v="131", "Chromium";v="131", "Not_A Brand";v="24"`,
		136: `"Chromium";v="136", "Google Chrome";v="136", "Not.A/Brand";v="99"`,
	}
	for major, want := range cases {
		if got := newProfile(fmt.Sprintf("%d.0.0.0", major), "Google Chrome", osTargets[0]).SecCHUA; got != want {
			t.Errorf("Chrome %d: got %s, want %s", major, got, want)
		}
	}
	if got := brandList(136, ""); len(got) != 2 || got[0].Name != "Not.A/Brand" || got[1].Name != "Chromium" {
		t.Errorf("unbranded Chromium 136 brands = %v", got)
	}
}

func TestNativeReportsRealVersionInHintsOnly(t *testing.T) {
	p := Native("148.0.7778.96", "")
	if !strings.Contains(p.UserAgent, "Chrome/148.0.0.0 ") {
		t.Errorf("UA must carry the reduced version: %s", p.UserAgent)
	}
	if p.FullVersion != "148.0.7778.96" {
		t.Errorf("full version must be the real build: %s", p.FullVersion)
	}
	for _, b := range p.Brands {
		if b.Name == "Google Chrome" {
			t.Errorf("an unbranded Chromium must not claim the Google Chrome brand: %v", p.Brands)
		}
	}
}
