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
	by := dataImpulseUsername(base, "https://shop.example.by/item", 0)
	if !strings.HasPrefix(by, base+"__cr.by;sessid.") {
		t.Fatalf("unexpected BY username: %s", by)
	}
	if by != dataImpulseUsername(base, "https://shop.example.by/other", 0) {
		t.Fatal("same domain should keep the same session")
	}
	pl := dataImpulseUsername(base, "https://example.pl/product", 0)
	if !strings.HasPrefix(pl, base+"__cr.pl;sessid.") {
		t.Fatalf("unexpected PL username: %s", pl)
	}
	if com := dataImpulseUsername(base, "https://www.zara.com/pl/item.html", 0); !strings.HasPrefix(com, base+"__cr.pl;sessid.") {
		t.Fatalf("hosts without a RU/BY storefront must exit in PL to match the browser's locale: %s", com)
	}
}

func TestRotateResidentialSessionChangesOnlyThatHost(t *testing.T) {
	base := "account-login"
	pl := "https://www.rotation-test.pl/product"
	other := "https://shop.rotation-test.by/item"
	before := dataImpulseUsername(base, pl, ResidentialGeneration(pl))
	otherBefore := dataImpulseUsername(base, other, ResidentialGeneration(other))

	// A different Store instance — every PageFetcher builds its own — must still move the
	// host for everyone.
	NewStore(nil).RotateResidentialSession(pl + "?variant=2")

	after := dataImpulseUsername(base, pl, ResidentialGeneration(pl))
	if after == before {
		t.Fatal("rotation must move the host onto a new sticky session")
	}
	if !strings.HasPrefix(after, base+"__cr.pl;sessid.") {
		t.Fatalf("rotation must keep country targeting: %s", after)
	}
	if got := dataImpulseUsername(base, other, ResidentialGeneration(other)); got != otherBefore {
		t.Fatal("rotating one host must not disturb another host's session")
	}
}

func TestPickedProxyClientURL(t *testing.T) {
	cases := []struct {
		name string
		p    PickedProxy
		want string
	}{
		{"public socks5 can't answer CONNECT", PickedProxy{Address: "1.2.3.4:1080", Protocol: "socks5"}, "socks5h://1.2.3.4:1080"},
		{"public http proxy", PickedProxy{Address: "1.2.3.4:8080", Protocol: "http"}, "http://1.2.3.4:8080"},
		{"paid gateway stored as socks5 speaks CONNECT", PickedProxy{Address: "gw.example:823", Username: "u", Password: "p", Protocol: "socks5"}, "http://u:p@gw.example:823"},
	}
	for _, c := range cases {
		if got := c.p.ClientURL(); got != c.want {
			t.Errorf("%s: ClientURL() = %q, want %q", c.name, got, c.want)
		}
	}
}
