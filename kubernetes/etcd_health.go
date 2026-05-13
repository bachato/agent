package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const (
	etcdHealthyPrefix   = "[+]etcd"
	etcdUnhealthyPrefix = "[-]etcd"
)

// CollectEtcdHealth calls /readyz?verbose on the API server and returns true
// when the etcd health check line reports healthy, false when it reports
// unhealthy. It returns an error when etcd health cannot be derived.
func CollectEtcdHealth(ctx context.Context, kc *KubeClient) (bool, error) {
	body, err := kc.cli.RESTClient().Get().
		AbsPath("/readyz").
		Param("verbose", "").
		DoRaw(ctx)

	return deriveEtcdHealthFromReadyzResponse(string(body), err)
}

func deriveEtcdHealthFromReadyzResponse(body string, requestErr error) (bool, error) {
	// DoRaw can return body and a non-nil error on non-2xx responses. Parse the
	// body first so we still capture explicit etcd health lines when present.
	if healthy, found := parseEtcdHealthLine(body); found {
		return healthy, nil
	}

	if requestErr != nil {
		return false, fmt.Errorf("failed to query /readyz?verbose. Error: %w", requestErr)
	}

	return false, errors.New("failed to derive etcd health from /readyz?verbose response")
}

func parseEtcdHealthLine(body string) (healthy bool, found bool) {
	for raw := range strings.SplitSeq(body, "\n") {
		line := strings.TrimSpace(raw)

		switch {
		case hasEtcdPrefix(line, etcdHealthyPrefix):
			if strings.Contains(line, "excluded:") {
				return false, false
			}
			return true, true
		case hasEtcdPrefix(line, etcdUnhealthyPrefix):
			return false, true
		}
	}

	return false, false
}

func hasEtcdPrefix(line, prefix string) bool {
	if !strings.HasPrefix(line, prefix) {
		return false
	}

	if len(line) == len(prefix) {
		return true
	}

	return line[len(prefix)] == ' '
}
