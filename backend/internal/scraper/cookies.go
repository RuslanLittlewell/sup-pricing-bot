package scraper

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

type CookieJar struct {
	Cookies []Cookie `json:"cookies"`
}

type Cookie struct {
	Domain         string  `json:"domain"`
	ExpirationDate float64 `json:"expirationDate"`
	HTTPOnly       bool    `json:"httpOnly"`
	Name           string  `json:"name"`
	Path           string  `json:"path"`
	SameSite       string  `json:"sameSite"`
	Secure         bool    `json:"secure"`
	Session        bool    `json:"session"`
	Value          string  `json:"value"`
}

func LoadCookies(path string) ([]Cookie, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read scraper cookies: %w", err)
	}

	var jar CookieJar
	if err := json.Unmarshal(data, &jar); err != nil {
		return nil, fmt.Errorf("parse scraper cookies: %w", err)
	}

	cookies := make([]Cookie, 0, len(jar.Cookies))
	now := float64(time.Now().Unix())
	for _, cookie := range jar.Cookies {
		if cookie.Name == "" {
			continue
		}
		if !cookie.Session && cookie.ExpirationDate > 0 && cookie.ExpirationDate < now {
			continue
		}
		if cookie.Path == "" {
			cookie.Path = "/"
		}
		cookies = append(cookies, cookie)
	}

	return cookies, nil
}

func HeaderForURL(cookies []Cookie, rawURL string) string {
	if len(cookies) == 0 {
		return ""
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return ""
	}

	var parts []string
	for _, cookie := range cookies {
		if !domainMatches(host, cookie.Domain) {
			continue
		}
		parts = append(parts, cookie.Name+"="+cookie.Value)
	}

	return strings.Join(parts, "; ")
}

func domainMatches(host, domain string) bool {
	domain = strings.TrimPrefix(strings.ToLower(domain), ".")
	return host == domain || strings.HasSuffix(host, "."+domain)
}
