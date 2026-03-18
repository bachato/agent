package serf

import (
	"testing"
	"time"

	"github.com/hashicorp/serf/serf"
	"github.com/portainer/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewClusterService verifies that NewClusterService stores the runtime config
// and leaves clusterAddr empty (non-Swarm path).
func TestNewClusterService(t *testing.T) {
	cfg := &agent.RuntimeConfig{NodeName: "n1"}
	svc := NewClusterService(cfg)
	assert.Equal(t, cfg, svc.runtimeConfiguration)
	assert.Empty(t, svc.clusterAddr)
}

// TestNewSwarmClusterService verifies that NewSwarmClusterService stores both the
// runtime config and the clusterAddr used for self-healing DNS re-resolution.
func TestNewSwarmClusterService(t *testing.T) {
	cfg := &agent.RuntimeConfig{NodeName: "n1"}
	svc := NewSwarmClusterService(cfg, "tasks.agent")
	assert.Equal(t, cfg, svc.runtimeConfiguration)
	assert.Equal(t, "tasks.agent", svc.clusterAddr)
}

// TestConvertRuntimeConfigurationToTagMap verifies that all four conditional branches
// in convertRuntimeConfigurationToTagMap produce the correct Serf tag map.
func TestConvertRuntimeConfigurationToTagMap(t *testing.T) {
	tests := []struct {
		name     string
		input    *agent.RuntimeConfig
		expected map[string]string
	}{
		{
			name: "standalone worker, no edge key, not leader",
			input: &agent.RuntimeConfig{
				AgentPort: "9001",
				NodeName:  "node-1",
				DockerConfig: agent.DockerRuntimeConfig{
					EngineType: agent.EngineTypeStandalone,
					NodeRole:   agent.NodeRoleWorker,
					Leader:     false,
				},
			},
			expected: map[string]string{
				memberTagKeyAgentPort:    "9001",
				memberTagKeyNodeName:     "node-1",
				memberTagKeyEngineStatus: memberTagValueEngineStatusStandalone,
				memberTagKeyNodeRole:     memberTagValueNodeRoleWorker,
			},
		},
		{
			name: "swarm manager, leader, edge key set",
			input: &agent.RuntimeConfig{
				AgentPort:  "9001",
				NodeName:   "node-2",
				EdgeKeySet: true,
				DockerConfig: agent.DockerRuntimeConfig{
					EngineType: agent.EngineTypeSwarm,
					NodeRole:   agent.NodeRoleManager,
					Leader:     true,
				},
			},
			expected: map[string]string{
				memberTagKeyAgentPort:    "9001",
				memberTagKeyNodeName:     "node-2",
				memberTagKeyEngineStatus: memberTagValueEngineStatusSwarm,
				memberTagKeyNodeRole:     memberTagValueNodeRoleManager,
				memberTagKeyIsLeader:     "1",
				memberTagKeyEdgeKeySet:   "set",
			},
		},
		{
			name: "swarm worker, not leader, no edge key",
			input: &agent.RuntimeConfig{
				AgentPort: "9001",
				NodeName:  "node-3",
				DockerConfig: agent.DockerRuntimeConfig{
					EngineType: agent.EngineTypeSwarm,
					NodeRole:   agent.NodeRoleWorker,
					Leader:     false,
				},
			},
			expected: map[string]string{
				memberTagKeyAgentPort:    "9001",
				memberTagKeyNodeName:     "node-3",
				memberTagKeyEngineStatus: memberTagValueEngineStatusSwarm,
				memberTagKeyNodeRole:     memberTagValueNodeRoleWorker,
			},
		},
		{
			name: "swarm manager, not leader, no edge key",
			input: &agent.RuntimeConfig{
				AgentPort: "9001",
				NodeName:  "node-4",
				DockerConfig: agent.DockerRuntimeConfig{
					EngineType: agent.EngineTypeSwarm,
					NodeRole:   agent.NodeRoleManager,
					Leader:     false,
				},
			},
			expected: map[string]string{
				memberTagKeyAgentPort:    "9001",
				memberTagKeyNodeName:     "node-4",
				memberTagKeyEngineStatus: memberTagValueEngineStatusSwarm,
				memberTagKeyNodeRole:     memberTagValueNodeRoleManager,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := convertRuntimeConfigurationToTagMap(tt.input)
			assert.Equal(t, tt.expected, got)
		})
	}
}

// TestClusterService_Leave verifies that Leave() is safe to call on a nil cluster
// and leaves a live cluster without error.
func TestClusterService_Leave(t *testing.T) {
	t.Run("no-op when cluster is nil", func(t *testing.T) {
		svc := NewClusterService(&agent.RuntimeConfig{NodeName: "n"})
		require.NotPanics(t, func() { svc.Leave() })
	})

	t.Run("leaves live cluster gracefully", func(t *testing.T) {
		s, _, _, _ := createSerfNode(t, "solo", workerTags())

		svc := NewClusterService(&agent.RuntimeConfig{NodeName: "solo"})
		svc.cluster = s

		require.NotPanics(t, func() { svc.Leave() })
	})
}

// TestClusterService_MemberLookups tests Members, GetMemberByRole, GetMemberByNodeName,
// and GetMemberWithEdgeKeySet against a live 2-node in-process cluster.
func TestClusterService_MemberLookups(t *testing.T) {
	// Manager node — tagged with role=manager and AgentPort.
	mgrTags := map[string]string{
		memberTagKeyNodeRole:  memberTagValueNodeRoleManager,
		memberTagKeyNodeName:  "manager",
		memberTagKeyAgentPort: "9001",
	}
	mgrSerf, managerAddr, _, _ := createSerfNode(t, "manager", mgrTags)

	// Worker node — tagged with role=worker, NodeName, and EdgeKeySet.
	wrkTags := map[string]string{
		memberTagKeyNodeRole:   memberTagValueNodeRoleWorker,
		memberTagKeyNodeName:   "worker-1",
		memberTagKeyAgentPort:  "9001",
		memberTagKeyEdgeKeySet: "set",
	}
	wrkSerf, _, _, _ := createSerfNode(t, "worker-1", wrkTags)

	_, err := wrkSerf.Join([]string{managerAddr}, true)
	require.NoError(t, err)

	// Build ClusterService backed by the manager's Serf instance.
	svc := newManagerService(mgrSerf)

	require.Eventually(t, func() bool {
		return numAliveMembers(mgrSerf) == 2
	}, 3*rejoinInterval, rejoinInterval/10, "manager should see both nodes as alive")

	t.Run("GetMemberByRole returns manager", func(t *testing.T) {
		m := svc.GetMemberByRole(agent.NodeRoleManager)
		require.NotNil(t, m)
		assert.Equal(t, memberTagValueNodeRoleManager, m.NodeRole)
	})

	t.Run("GetMemberByRole returns worker", func(t *testing.T) {
		m := svc.GetMemberByRole(agent.NodeRoleWorker)
		require.NotNil(t, m)
		assert.Equal(t, memberTagValueNodeRoleWorker, m.NodeRole)
	})

	t.Run("GetMemberByRole returns nil when role absent", func(t *testing.T) {
		// Spin up an isolated single-worker node with no manager in its cluster.
		soloAddr, soloLn := freeListener(t)
		require.NoError(t, soloLn.Close())
		soloConf := testSerfConfig(t, "solo-worker", soloAddr, map[string]string{
			memberTagKeyNodeRole: memberTagValueNodeRoleWorker,
			memberTagKeyNodeName: "solo-worker",
		})
		soloSerf, soloErr := serf.Create(soloConf)
		require.NoError(t, soloErr)
		t.Cleanup(func() { soloSerf.Shutdown() }) //nolint:errcheck
		soloSvc := &ClusterService{cluster: soloSerf}
		assert.Nil(t, soloSvc.GetMemberByRole(agent.NodeRoleManager))
	})

	t.Run("GetMemberByNodeName found", func(t *testing.T) {
		m := svc.GetMemberByNodeName("worker-1")
		require.NotNil(t, m)
		assert.Equal(t, "worker-1", m.NodeName)
	})

	t.Run("GetMemberByNodeName missing", func(t *testing.T) {
		assert.Nil(t, svc.GetMemberByNodeName("ghost"))
	})

	t.Run("GetMemberWithEdgeKeySet found", func(t *testing.T) {
		m := svc.GetMemberWithEdgeKeySet()
		require.NotNil(t, m)
		assert.True(t, m.EdgeKeySet)
	})

	t.Run("GetMemberWithEdgeKeySet missing", func(t *testing.T) {
		// Only look at manager-tagged nodes — build a service that only sees a manager without EdgeKeySet.
		// Override tags on manager to remove EdgeKeySet — easiest: use a 1-node cluster.
		soloAddr, soloLn := freeListener(t)
		require.NoError(t, soloLn.Close())
		soloConf := testSerfConfig(t, "solo", soloAddr, map[string]string{
			memberTagKeyNodeRole: memberTagValueNodeRoleManager,
			memberTagKeyNodeName: "solo",
		})
		soloSerf, soloErr := serf.Create(soloConf)
		require.NoError(t, soloErr)
		t.Cleanup(func() { soloSerf.Shutdown() }) //nolint:errcheck
		noEdgeSvc := &ClusterService{cluster: soloSerf}
		assert.Nil(t, noEdgeSvc.GetMemberWithEdgeKeySet())
	})

	t.Run("Members excludes non-alive nodes", func(t *testing.T) {
		// Leave the worker so it transitions away from StatusAlive.
		wrkSerf.Leave() //nolint:errcheck
		require.Eventually(t, func() bool {
			for _, m := range svc.Members() {
				if m.NodeName == "worker-1" {
					return false
				}
			}
			return true
		}, 5*rejoinInterval, rejoinInterval/10, "left worker should no longer appear in Members()")
	})
}

// TestClusterService_Create verifies the Create() method across several scenarios.
func TestClusterService_Create(t *testing.T) {
	const probeTimeout = 25 * time.Millisecond
	const probeInterval = 50 * time.Millisecond

	t.Run("standalone node starts successfully", func(t *testing.T) {
		svc := NewClusterService(&agent.RuntimeConfig{NodeName: "solo"})
		err := svc.Create("127.0.0.1", nil, probeTimeout, probeInterval)
		require.NoError(t, err)
		t.Cleanup(func() { svc.cluster.Shutdown() }) //nolint:errcheck
		assert.NotNil(t, svc.cluster)
		assert.False(t, svc.rejoining.Load())
	})

	t.Run("swarm node starts with clusterAddr set", func(t *testing.T) {
		svc := NewSwarmClusterService(&agent.RuntimeConfig{NodeName: "swarm-node"}, "tasks.agent")
		err := svc.Create("127.0.0.1", nil, probeTimeout, probeInterval)
		require.NoError(t, err)
		t.Cleanup(func() { svc.cluster.Shutdown() }) //nolint:errcheck
		assert.NotNil(t, svc.cluster)
	})

	t.Run("empty joinAddr is a no-op", func(t *testing.T) {
		svc := NewSwarmClusterService(&agent.RuntimeConfig{NodeName: "bootstrap"}, "tasks.agent")
		err := svc.Create("127.0.0.1", []string{}, probeTimeout, probeInterval)
		require.NoError(t, err)
		t.Cleanup(func() { svc.cluster.Shutdown() }) //nolint:errcheck
		assert.NotNil(t, svc.cluster)
	})

	t.Run("unreachable joinAddr is non-fatal", func(t *testing.T) {
		svc := NewSwarmClusterService(&agent.RuntimeConfig{NodeName: "node-a"}, "tasks.agent")
		err := svc.Create("127.0.0.1", []string{"127.0.0.1:19999"}, probeTimeout, probeInterval)
		require.NoError(t, err)
		t.Cleanup(func() { svc.cluster.Shutdown() }) //nolint:errcheck
		assert.NotNil(t, svc.cluster)
	})

	t.Run("node tags reflect runtime configuration", func(t *testing.T) {
		svc := NewSwarmClusterService(&agent.RuntimeConfig{
			NodeName: "mgr",
			DockerConfig: agent.DockerRuntimeConfig{
				NodeRole:   agent.NodeRoleManager,
				EngineType: agent.EngineTypeSwarm,
				Leader:     true,
			},
		}, "tasks.agent")
		err := svc.Create("127.0.0.1", nil, probeTimeout, probeInterval)
		require.NoError(t, err)
		t.Cleanup(func() { svc.cluster.Shutdown() }) //nolint:errcheck

		tags := svc.cluster.LocalMember().Tags
		assert.Equal(t, memberTagValueNodeRoleManager, tags[memberTagKeyNodeRole])
		assert.Equal(t, memberTagValueEngineStatusSwarm, tags[memberTagKeyEngineStatus])
		assert.Equal(t, "1", tags[memberTagKeyIsLeader])
	})
}

// TestClusterService_WatchClusterEvents_Filtering verifies that watchClusterEvents
// only triggers the rejoin loop for EventMemberReap events on manager nodes.
func TestClusterService_WatchClusterEvents_Filtering(t *testing.T) {
	newSvc := func(t *testing.T) (*ClusterService, chan serf.Event) {
		addr, ln := freeListener(t)
		require.NoError(t, ln.Close())
		s, err := serf.Create(testSerfConfig(t, "worker", addr, workerTags()))
		require.NoError(t, err)

		eventCh := make(chan serf.Event, 16)
		t.Cleanup(func() { close(eventCh) })
		t.Cleanup(func() { s.Shutdown() }) //nolint:errcheck

		svc := newWorkerService(t, s, eventCh, "localhost", rejoinInterval)
		return svc, eventCh
	}

	t.Run("non-MemberEvent does not trigger rejoin", func(t *testing.T) {
		svc, eventCh := newSvc(t)
		eventCh <- serf.UserEvent{Name: "test"}
		assert.Never(t, func() bool {
			return svc.rejoining.Load()
		}, 2*rejoinInterval, rejoinInterval/10, "non-member events must not trigger rejoin loop")
	})

	t.Run("EventMemberJoin does not trigger rejoin", func(t *testing.T) {
		svc, eventCh := newSvc(t)
		eventCh <- serf.MemberEvent{Type: serf.EventMemberJoin}
		assert.Never(t, func() bool {
			return svc.rejoining.Load()
		}, 2*rejoinInterval, rejoinInterval/10, "join events must not trigger rejoin loop")
	})

	t.Run("EventMemberReap on worker does not trigger rejoin", func(t *testing.T) {
		svc, eventCh := newSvc(t)
		eventCh <- serf.MemberEvent{
			Type: serf.EventMemberReap,
			Members: []serf.Member{
				{Tags: map[string]string{memberTagKeyNodeRole: memberTagValueNodeRoleWorker}},
			},
		}
		assert.Never(t, func() bool {
			return svc.rejoining.Load()
		}, 2*rejoinInterval, rejoinInterval/10, "worker reap must not trigger rejoin loop")
	})

	t.Run("EventMemberReap on manager triggers rejoin", func(t *testing.T) {
		svc, eventCh := newSvc(t)
		eventCh <- serf.MemberEvent{
			Type: serf.EventMemberReap,
			Members: []serf.Member{
				{Tags: map[string]string{memberTagKeyNodeRole: memberTagValueNodeRoleManager}},
			},
		}
		require.Eventually(t, func() bool {
			return svc.rejoining.Load()
		}, 3*rejoinInterval, rejoinInterval/10, "reap of manager should trigger rejoin loop")
	})

	t.Run("second reap does not start duplicate rejoin goroutine", func(t *testing.T) {
		svc, eventCh := newSvc(t)
		reapEvent := serf.MemberEvent{
			Type: serf.EventMemberReap,
			Members: []serf.Member{
				{Tags: map[string]string{memberTagKeyNodeRole: memberTagValueNodeRoleManager}},
			},
		}
		// Send two reap events; only one goroutine should start (CompareAndSwap guard).
		eventCh <- reapEvent
		eventCh <- reapEvent
		require.Eventually(t, func() bool {
			return svc.rejoining.Load()
		}, 3*rejoinInterval, rejoinInterval/10, "rejoin loop should be running")
		// rejoining flag stays true — it was set once and the loop is still running.
		assert.Never(t, func() bool {
			return !svc.rejoining.Load()
		}, rejoinInterval, rejoinInterval/10, "flag must stay set; loop is running")
	})
}
