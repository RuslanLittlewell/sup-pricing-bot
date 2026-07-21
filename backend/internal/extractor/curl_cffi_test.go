package extractor

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestCurlCFFIFetcher(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/fetch" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"X-Upstream-Status": []string{"200"}}, Body: io.NopCloser(strings.NewReader("product page"))}, nil
	})}

	fetcher := &CurlCFFIFetcher{baseURL: "http://curl-cffi:8080", client: client}
	body, err := fetcher.Fetch("https://example.com/item", "", map[string]string{"Accept-Language": "pl-PL"})
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "product page" {
		t.Fatalf("unexpected body %q", body)
	}
}

func TestCurlCFFIFetcherRejectsUpstreamBlock(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"X-Upstream-Status": []string{"403"}}, Body: io.NopCloser(strings.NewReader("Access Denied"))}, nil
	})}

	fetcher := &CurlCFFIFetcher{baseURL: "http://curl-cffi:8080", client: client}
	if _, err := fetcher.Fetch("https://example.com/item", "", nil); err == nil {
		t.Fatal("expected upstream 403 to be rejected")
	}
}

func TestCurlCFFIFetcherRejectsAkamaiSoftBlock(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		body := `<html><title>Access Denied</title><p>Reference #18.abc</p><a href="https://errors.edgesuite.net/18.abc">error</a></html>`
		return &http.Response{StatusCode: 200, Header: http.Header{"X-Upstream-Status": []string{"200"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}

	fetcher := &CurlCFFIFetcher{baseURL: "http://curl-cffi:8080", client: client}
	if _, err := fetcher.Fetch("https://example.com/item", "", nil); err == nil {
		t.Fatal("expected HTTP 200 Akamai deny page to be rejected")
	}
}
