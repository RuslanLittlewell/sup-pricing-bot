package proxypool

import "testing"

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
