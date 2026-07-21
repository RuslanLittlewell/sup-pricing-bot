package extractor

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestFirefoxFetcher(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/render" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("zara product"))}, nil
	})}
	fetcher := &FirefoxFetcher{baseURL: "http://firefox:8080", client: client}
	body, err := fetcher.Fetch("https://www.zara.com/item", "", nil)
	if err != nil || string(body) != "zara product" {
		t.Fatalf("body=%q err=%v", body, err)
	}
}

func TestIsZaraURL(t *testing.T) {
	if !isZaraURL("https://www.zara.com/pl/item") || isZaraURL("https://zara.com.example.org/item") {
		t.Fatal("unexpected Zara URL classification")
	}
}
