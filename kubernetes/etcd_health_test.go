package kubernetes

import (
	"errors"
	"strings"
	"testing"
)

func TestParseEtcdHealthLine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		body    string
		healthy bool
		found   bool
	}{
		{
			name:    "healthy etcd check",
			body:    "[+]ping ok\n[+]etcd ok\nhealthz check passed\n",
			healthy: true,
			found:   true,
		},
		{
			name:    "unhealthy etcd check",
			body:    "[+]ping ok\n[-]etcd failed: context deadline exceeded\nhealthz check failed\n",
			healthy: false,
			found:   true,
		},
		{
			name:    "excluded etcd check is treated as not found",
			body:    "[+]ping ok\n[+]etcd excluded: ok\nhealthz check passed\n",
			healthy: false,
			found:   false,
		},
		{
			name:    "ignores unrelated lines containing etcd substring",
			body:    "[+]poststarthook/start-etcd-helpers ok\nhealthz check passed\n",
			healthy: false,
			found:   false,
		},
		{
			name:    "no etcd line",
			body:    "[+]ping ok\n[+]log ok\nhealthz check passed\n",
			healthy: false,
			found:   false,
		},
	}

	for _, tt := range tests {
		tc := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			healthy, found := parseEtcdHealthLine(tc.body)
			if healthy != tc.healthy {
				t.Fatalf("healthy mismatch: got %v, want %v", healthy, tc.healthy)
			}
			if found != tc.found {
				t.Fatalf("found mismatch: got %v, want %v", found, tc.found)
			}
		})
	}
}

func TestDeriveEtcdHealthFromReadyzResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		body        string
		requestErr  error
		healthy     bool
		wantErr     bool
		errContains string
	}{
		{
			name:    "healthy etcd line",
			body:    "[+]ping ok\n[+]etcd ok\nhealthz check passed\n",
			healthy: true,
		},
		{
			name:    "unhealthy etcd line",
			body:    "[+]ping ok\n[-]etcd failed: context deadline exceeded\nhealthz check failed\n",
			healthy: false,
		},
		{
			name:        "non-2xx body still parsed when etcd line exists",
			body:        "[+]ping ok\n[-]etcd failed: timeout\n",
			requestErr:  errors.New("503 service unavailable"),
			healthy:     false,
			wantErr:     false,
			errContains: "",
		},
		{
			name:        "request error with no etcd line returns query error",
			body:        "",
			requestErr:  errors.New("connection refused"),
			healthy:     false,
			wantErr:     true,
			errContains: "failed to query /readyz?verbose",
		},
		{
			name:        "excluded etcd line is indeterminate",
			body:        "[+]ping ok\n[+]etcd excluded: ok\nhealthz check passed\n",
			healthy:     false,
			wantErr:     true,
			errContains: "failed to derive etcd health",
		},
		{
			name:        "no etcd line is indeterminate",
			body:        "[+]ping ok\n[+]log ok\nhealthz check passed\n",
			healthy:     false,
			wantErr:     true,
			errContains: "failed to derive etcd health",
		},
	}

	for _, tt := range tests {
		tc := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			healthy, err := deriveEtcdHealthFromReadyzResponse(tc.body, tc.requestErr)
			if healthy != tc.healthy {
				t.Fatalf("healthy mismatch: got %v, want %v", healthy, tc.healthy)
			}

			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tc.errContains != "" && !strings.Contains(err.Error(), tc.errContains) {
					t.Fatalf("error mismatch: got %q, want substring %q", err.Error(), tc.errContains)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
