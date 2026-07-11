package proxypool

import (
	"strings"
	"testing"
)

func TestPickedProxyURL(t *testing.T) {
	cases := []struct {
		name string
		p    PickedProxy
		want string
	}{
		{
			name: "unauthenticated",
			p:    PickedProxy{Address: "1.2.3.4:1080"},
			want: "socks5://1.2.3.4:1080",
		},
		{
			name: "authenticated",
			p:    PickedProxy{Address: "1.2.3.4:1080", Username: "user", Password: "pass"},
			want: "socks5://user:pass@1.2.3.4:1080",
		},
		{
			name: "credentials needing escaping",
			p:    PickedProxy{Address: "1.2.3.4:1080", Username: "u@ser", Password: "p@ss:word"},
			want: "socks5://u%40ser:p%40ss%3Aword@1.2.3.4:1080",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.p.URL(); got != c.want {
				t.Errorf("URL() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestPickedProxyHTTPURL(t *testing.T) {
	p := PickedProxy{Address: "1.2.3.4:1080", Username: "user", Password: "pass"}
	if got, want := p.HTTPURL(), "http://user:pass@1.2.3.4:1080"; got != want {
		t.Errorf("HTTPURL() = %q, want %q", got, want)
	}
	if got, want := p.URL(), "socks5://user:pass@1.2.3.4:1080"; got != want {
		t.Errorf("URL() = %q, want %q (should stay socks5, unaffected by HTTPURL)", got, want)
	}
}

func TestRequiresRussianProxy(t *testing.T) {
	cases := map[string]bool{
		"https://example.ru/product":   true,
		"https://shop.example.by/item": true,
		"https://example.ru.com":       false,
		"https://example.com/path.ru":  false,
		"not a url":                    false,
	}
	for rawURL, want := range cases {
		if got := RequiresRussianProxy(rawURL); got != want {
			t.Errorf("RequiresRussianProxy(%q) = %v, want %v", rawURL, got, want)
		}
	}
}

func TestDataImpulseUsernameTargetsCountryAndKeepsDomainSession(t *testing.T) {
	base := "account-login"
	by := dataImpulseUsername(base, "https://shop.example.by/item")
	if !strings.HasPrefix(by, base+"__cr.by;sessid.") {
		t.Fatalf("unexpected BY username: %s", by)
	}
	if by != dataImpulseUsername(base, "https://shop.example.by/other") {
		t.Fatal("same domain should keep the same session")
	}
	pl := dataImpulseUsername(base, "https://example.pl/product")
	if !strings.HasPrefix(pl, base+"__cr.pl;sessid.") {
		t.Fatalf("unexpected PL username: %s", pl)
	}
}
