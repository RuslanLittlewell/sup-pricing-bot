// Package useragent provides a pool of browser fingerprint profiles shared by the
// direct-HTTP fetcher (uTLS) and the headless-renderer paths, so both can present a
// randomized-but-internally-consistent browser identity per request instead of the same
// fixed User-Agent every time.
package useragent

import "math/rand"

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

var profiles = []Profile{
	{
		UserAgent:         "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36",
		SecCHUA:           `"Chromium";v="126", "Google Chrome";v="126", "Not-A.Brand";v="99"`,
		SecCHUAPlatform:   `"Windows"`,
		CDPPlatform:       "Windows",
		NavigatorPlatform: "Win32",
	},
	{
		UserAgent:         "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/127.0.0.0 Safari/537.36",
		SecCHUA:           `"Chromium";v="127", "Google Chrome";v="127", "Not-A.Brand";v="99"`,
		SecCHUAPlatform:   `"Windows"`,
		CDPPlatform:       "Windows",
		NavigatorPlatform: "Win32",
	},
	{
		UserAgent:         "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36",
		SecCHUA:           `"Chromium";v="128", "Google Chrome";v="128", "Not-A.Brand";v="99"`,
		SecCHUAPlatform:   `"Windows"`,
		CDPPlatform:       "Windows",
		NavigatorPlatform: "Win32",
	},
	{
		UserAgent:         "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36",
		SecCHUA:           `"Chromium";v="126", "Google Chrome";v="126", "Not-A.Brand";v="99"`,
		SecCHUAPlatform:   `"macOS"`,
		CDPPlatform:       "macOS",
		NavigatorPlatform: "MacIntel",
	},
	{
		UserAgent:         "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36",
		SecCHUA:           `"Chromium";v="125", "Google Chrome";v="125", "Not-A.Brand";v="99"`,
		SecCHUAPlatform:   `"macOS"`,
		CDPPlatform:       "macOS",
		NavigatorPlatform: "MacIntel",
	},
	{
		UserAgent:         "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36",
		SecCHUA:           `"Chromium";v="124", "Google Chrome";v="124", "Not-A.Brand";v="99"`,
		SecCHUAPlatform:   `"Linux"`,
		CDPPlatform:       "Linux",
		NavigatorPlatform: "Linux x86_64",
	},
}

// Random returns one of the pool of browser fingerprint profiles, picked uniformly at
// random.
func Random() Profile {
	return profiles[rand.Intn(len(profiles))]
}
