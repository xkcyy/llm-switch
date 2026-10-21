package protocol

import (
	"net/http"
	"testing"
)

func TestNormalizeUpstreamErrorStatus(t *testing.T) {
	cloudflareHTML := `<html><head><title>400 Bad Request</title></head><body><center><h1>400 Bad Request</h1></center><hr><center>cloudflare</center></body></html>`
	tests := []struct {
		name   string
		status int
		body   string
		want   int
	}{
		{name: "cloudflare html 400", status: http.StatusBadRequest, body: cloudflareHTML, want: http.StatusBadGateway},
		{name: "ordinary json 400", status: http.StatusBadRequest, body: `{"error":{"message":"bad request"}}`, want: http.StatusBadRequest},
		{name: "html without cloudflare", status: http.StatusBadRequest, body: `<html><body>bad request</body></html>`, want: http.StatusBadRequest},
		{name: "cloudflare 403 unchanged", status: http.StatusForbidden, body: cloudflareHTML, want: http.StatusForbidden},
		{name: "cloudflare 503 unchanged", status: http.StatusServiceUnavailable, body: cloudflareHTML, want: http.StatusServiceUnavailable},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeUpstreamErrorStatus(tc.status, []byte(tc.body)); got != tc.want {
				t.Fatalf("status = %d, want %d", got, tc.want)
			}
		})
	}
}
