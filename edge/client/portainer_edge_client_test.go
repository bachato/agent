package client

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/portainer/agent"
	portainer "github.com/portainer/portainer/api"
	"github.com/portainer/portainer/pkg/fips"

	"github.com/stretchr/testify/require"
)

func TestGetEdgeConfig(t *testing.T) {
	fips.InitFIPS(false)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"Id": 1, "Name": "test"}`))
	}))
	defer srv.Close()

	client := &PortainerEdgeClient{
		getEndpointIDFn: func() portainer.EndpointID { return 1 },
		httpClient:      BuildHTTPClient(30, &agent.Options{}),
		serverAddress:   srv.URL,
	}

	edgeConfig, err := client.GetEdgeConfig(EdgeConfigID(1))
	require.NoError(t, err)
	require.Equal(t, edgeConfig.ID, EdgeConfigID(1))
	require.Equal(t, "test", edgeConfig.Name)
}

func TestMutateResponseForCaching(t *testing.T) {
	// Create a test response with stacks that have ForceRedeploy set
	originalResp := PollStatusResponse{
		Stacks: []StackStatus{
			{ID: 1, Name: "stack1", ForceRedeploy: true},
			{ID: 2, Name: "stack2", ForceRedeploy: false},
			{ID: 3, Name: "stack3", ForceRedeploy: true},
		},
	}

	respForCache := mutateResponseForCaching(&originalResp)

	// Test response for cache
	require.Len(t, respForCache.Stacks, 3)
	require.Equal(t, 1, respForCache.Stacks[0].ID)
	require.Equal(t, "stack1", respForCache.Stacks[0].Name)
	// Should be mutated to false
	require.False(t, respForCache.Stacks[0].ForceRedeploy)

	require.Equal(t, 2, respForCache.Stacks[1].ID)
	require.Equal(t, "stack2", respForCache.Stacks[1].Name)
	// Should remain false
	require.False(t, respForCache.Stacks[1].ForceRedeploy)

	require.Equal(t, 3, respForCache.Stacks[2].ID)
	require.Equal(t, "stack3", respForCache.Stacks[2].Name)
	// Should be mutated to false
	require.False(t, respForCache.Stacks[2].ForceRedeploy)

	// Test that modifying the original doesn't affect the cloned responses
	require.Len(t, originalResp.Stacks, 3)
	require.Equal(t, 1, originalResp.Stacks[0].ID)
	require.Equal(t, "stack1", originalResp.Stacks[0].Name)
	require.True(t, originalResp.Stacks[0].ForceRedeploy)

	require.Equal(t, 2, originalResp.Stacks[1].ID)
	require.Equal(t, "stack2", originalResp.Stacks[1].Name)
	require.False(t, originalResp.Stacks[1].ForceRedeploy)

	require.Equal(t, 3, originalResp.Stacks[2].ID)
	require.Equal(t, "stack3", originalResp.Stacks[2].Name)
	require.True(t, originalResp.Stacks[2].ForceRedeploy)
}
