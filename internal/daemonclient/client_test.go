package daemonclient

import "testing"

func TestEndpointBoundary(t *testing.T) {
	for _, endpoint := range []string{"http://127.0.0.1:8931", "http://localhost:8931/", "http://[::1]:8931"} {
		client, err := New(endpoint)
		if err != nil {
			t.Errorf("%s: %v", endpoint, err)
		} else {
			client.Close()
		}
	}
	for _, endpoint := range []string{"https://127.0.0.1:8931", "http://example.com", "http://192.168.1.1", "http://user:pass@localhost", "http://localhost/v1", "http://localhost?token=x", "http://localhost#x", ":bad"} {
		if client, err := New(endpoint); err == nil {
			client.Close()
			t.Errorf("accepted %s", endpoint)
		}
	}
}
