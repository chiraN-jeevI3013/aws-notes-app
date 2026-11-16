package api

// Unit tests: the router with no AWS behind it (Store/Photos are nil).
// They cover what runs BEFORE any database call: routing, CORS, input validation.
// End-to-end behavior against DynamoDB/S3 is covered by scripts/smoke.sh and photo-test.sh.

import (
	"net/http/httptest"
	"strings"
	"testing"
)

const origin = "http://example.test"

func TestRouter(t *testing.T) {
	h := NewRouter(&Server{}, origin)
	tests := []struct {
		name, method, path, body string
		want                     int
	}{
		{"health", "GET", "/health", "", 200},
		{"preflight", "OPTIONS", "/folders", "", 204},
		{"folder empty name", "POST", "/folders", `{"name":"  "}`, 400},
		{"folder name too long", "POST", "/folders", `{"name":"` + strings.Repeat("a", 101) + `"}`, 400},
		{"folder bad json", "POST", "/folders", `{not json`, 400},
		{"note missing title", "POST", "/folders/f1/notes", `{"text":"hi"}`, 400},
		{"note title too long", "POST", "/folders/f1/notes", `{"title":"` + strings.Repeat("a", 201) + `"}`, 400},
		{"unknown route", "GET", "/nope", "", 404},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder() // fake ResponseWriter: no network, no port
			h.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("%s %s: got %d, want %d (body: %s)", tc.method, tc.path, rec.Code, tc.want, rec.Body)
			}
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != origin {
				t.Errorf("CORS origin = %q, want %q", got, origin)
			}
		})
	}
}
