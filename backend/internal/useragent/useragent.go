// Package useragent provides a pool of browser fingerprint profiles shared by the
// direct-HTTP fetcher (uTLS) and the headless-renderer paths, so both can present a
// randomized-but-internally-consistent browser identity per request instead of the same
// fixed User-Agent every time.
package useragent

import (
	"fmt"
	"math/rand"
	"strings"
)

// Profile bundles a User-Agent string together with the Client Hints (Sec-CH-UA*)
// header values and navigator.platform override that must match it. Presenting one
// without the others — e.g. a macOS User-Agent alongside a Windows Sec-CH-UA-Platform —
// is itself the kind of inconsistency bot-detection systems check for, so callers should
// take a single Profile and apply all of its fields together, never mixing fields from
// different profiles within one request or page load.
type Profile struct {
	UserAgent string
	// SecCHUA is the Sec-CH-UA header value, e.g. `"Chromium";v="126", "Google Chrome";v="126", "Not-A.Brand";v="99"`.
	SecCHUA string
	// SecCHUAPlatform is the Sec-CH-UA-Platform header value, quoted per spec, e.g. `"Windows"`.
	SecCHUAPlatform string
	// CDPPlatform is the platform string passed to Emulation.setUserAgentOverride.
	CDPPlatform string
	// NavigatorPlatform is the value the navigator.platform JS property should report, e.g. "Win32".
	NavigatorPlatform string
}

// chromeVersions covers the last several stable Chrome releases — recent enough that a
// real visitor plausibly has any one of them installed today, old enough to still be in
// the wild rather than a beta channel nobody uses. A widely-shared 2015-era UA list (e.g.
// the popular pzb/b4b6f57144aea7827ae4 gist) was considered as a source, but every entry
// in it is a 2015 browser version (Chrome 37-45, Firefox 31-40, IE9-11) — presenting one
// of those in 2026 is itself an anomaly a modern bot-detection system flags, and none of
// them have known Client Hints to accompany them, so a real gist import would've made the
// mask worse, not better. Generating a larger current, internally-consistent set instead.
var chromeVersions = []string{
	"124.0.0.0", "125.0.0.0", "126.0.0.0", "127.0.0.0",
	"128.0.0.0", "129.0.0.0", "130.0.0.0", "131.0.0.0",
}

type osTarget struct {
	uaPlatform        string // e.g. "Windows NT 10.0; Win64; x64"
	secCHUAPlatform   string
	cdpPlatform       string
	navigatorPlatform string
}

var osTargets = []osTarget{
	{uaPlatform: "Windows NT 10.0; Win64; x64", secCHUAPlatform: `"Windows"`, cdpPlatform: "Windows", navigatorPlatform: "Win32"},
	{uaPlatform: "Macintosh; Intel Mac OS X 10_15_7", secCHUAPlatform: `"macOS"`, cdpPlatform: "macOS", navigatorPlatform: "MacIntel"},
	{uaPlatform: "X11; Linux x86_64", secCHUAPlatform: `"Linux"`, cdpPlatform: "Linux", navigatorPlatform: "Linux x86_64"},
}

var profiles = buildProfiles()

func buildProfiles() []Profile {
	var out []Profile
	for _, version := range chromeVersions {
		major, _, _ := strings.Cut(version, ".")
		secCHUA := fmt.Sprintf(`"Chromium";v="%s", "Google Chrome";v="%s", "Not-A.Brand";v="99"`, major, major)
		for _, target := range osTargets {
			out = append(out, Profile{
				UserAgent:         fmt.Sprintf("Mozilla/5.0 (%s) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%s Safari/537.36", target.uaPlatform, version),
				SecCHUA:           secCHUA,
				SecCHUAPlatform:   target.secCHUAPlatform,
				CDPPlatform:       target.cdpPlatform,
				NavigatorPlatform: target.navigatorPlatform,
			})
		}
	}
	return out
}

// Random returns one of the pool of browser fingerprint profiles, picked uniformly at
// random.
func Random() Profile {
	return profiles[rand.Intn(len(profiles))]
}

// ByUserAgent looks up the full Profile (Client Hints included) for a previously-picked
// UserAgent string — used to replay a saved successful fingerprint (see
// proxypool.Store.GetFingerprint) with its matching headers, not just the bare string.
func ByUserAgent(ua string) (Profile, bool) {
	for _, p := range profiles {
		if p.UserAgent == ua {
			return p, true
		}
	}
	return Profile{}, false
}
