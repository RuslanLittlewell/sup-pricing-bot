package security

import "testing"

func TestSafeDialControlBlocksPrivate(t *testing.T) {
	blocked := []string{
		"127.0.0.1:80",
		"10.1.2.3:443",
		"192.168.0.5:8080",
		"169.254.169.254:80", // cloud metadata endpoint
		"[::1]:443",
	}
	for _, addr := range blocked {
		if err := SafeDialControl("tcp", addr, nil); err == nil {
			t.Errorf("expected %s to be blocked, got nil", addr)
		}
	}

	allowed := []string{
		"93.184.216.34:443", // example.com
		"1.1.1.1:443",
	}
	for _, addr := range allowed {
		if err := SafeDialControl("tcp", addr, nil); err != nil {
			t.Errorf("expected %s to be allowed, got %v", addr, err)
		}
	}
}
