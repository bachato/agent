package kubernetes

import "context"

// CollectAPIServerHealth probes /livez on the Kubernetes API server and returns
// true when the server reports healthy (200), false for any non-200 response or
// connection failure. A connection failure is treated as unhealthy because the
// agent uses the same REST client for all API calls — if /livez is unreachable,
// the API server is effectively down from the agent's perspective.
func CollectAPIServerHealth(ctx context.Context, kc *KubeClient) bool {
	_, err := kc.cli.RESTClient().Get().
		AbsPath("/livez").
		DoRaw(ctx)

	return err == nil
}
