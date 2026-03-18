package serf

import (
	"net"
	"testing"

	"github.com/portainer/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestClusterService_SelfHealingRejoin tests the full self-healing path:
//
//  1. A 3-node cluster (1 manager + 2 workers) forms successfully.
//  2. The manager shuts down hard (simulates container restart / node reboot).
//  3. Workers detect the reap and start the rejoin loop.
//  4. The manager restarts on the same address.
//  5. Workers reconnect to the manager — the rejoin loop stops only when a manager
//     is visible as StatusAlive, not merely when any peer is contacted.
func TestClusterService_SelfHealingRejoin(t *testing.T) {
	// ── Phase 1: bring up the initial 3-node cluster ──────────────────────────

	managerSerf, managerAddr, managerEventCh, managerCancel := createSerfNode(t, "manager", managerTags())
	worker1Serf, worker1Addr, worker1EventCh, _ := createSerfNode(t, "worker-1", workerTags())
	worker2Serf, _, worker2EventCh, _ := createSerfNode(t, "worker-2", workerTags())

	// Join all three into one cluster via the manager.
	_, err := worker1Serf.Join([]string{managerAddr}, true)
	require.NoError(t, err)
	_, err = worker2Serf.Join([]string{managerAddr}, true)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return numAliveMembers(managerSerf) == 3 &&
			numAliveMembers(worker1Serf) == 3 &&
			numAliveMembers(worker2Serf) == 3
	}, 3*rejoinInterval, rejoinInterval/10, "all three nodes should see each other as alive")

	// Wire up ClusterService wrappers for the two workers.
	// The restarted manager joins the workers directly, so the rejoin loop's
	// termination condition (GetMemberByRole(manager) != nil) is what's tested here.
	managerHost, _, err := net.SplitHostPort(managerAddr)
	require.NoError(t, err)

	_ = newWorkerService(t, managerSerf, managerEventCh, managerHost, rejoinInterval)
	worker1Svc := newWorkerService(t, worker1Serf, worker1EventCh, managerHost, rejoinInterval)
	worker2Svc := newWorkerService(t, worker2Serf, worker2EventCh, managerHost, rejoinInterval)

	// ── Phase 2: manager hard-stops (container restart) ──────────────────────

	managerCancel()

	// Workers should detect the manager as no longer alive.
	require.Eventually(t, func() bool {
		return worker1Svc.GetMemberByRole(agent.NodeRoleManager) == nil &&
			worker2Svc.GetMemberByRole(agent.NodeRoleManager) == nil
	}, 5*rejoinInterval, rejoinInterval/10, "workers should no longer see the manager as alive after shutdown")

	// The reap event is needed to trigger rejoinUntilManagerFound. Serf fires
	// EventMemberReap after TombstoneTimeout elapses (set to 1µs in test config).
	require.Eventually(t, func() bool {
		return worker1Svc.rejoining.Load() || worker2Svc.rejoining.Load()
	}, 5*rejoinInterval, rejoinInterval/10, "at least one worker should have started the rejoin loop after the manager is reaped")

	// ── Phase 3: manager restarts on the same address ─────────────────────────

	manager2Serf, _, manager2EventCh, _ := createSerfNodeAt(t, "manager", managerAddr, managerTags())
	_ = newWorkerService(t, manager2Serf, manager2EventCh, managerHost, rejoinInterval)

	// New manager joins the existing worker cluster.
	_, err = manager2Serf.Join([]string{worker1Addr}, true)
	require.NoError(t, err)

	// ── Phase 4: workers should see the new manager ───────────────────────────

	require.Eventually(t, func() bool {
		return worker1Svc.GetMemberByRole(agent.NodeRoleManager) != nil &&
			worker2Svc.GetMemberByRole(agent.NodeRoleManager) != nil
	}, 5*rejoinInterval, rejoinInterval/10, "both workers should see the new manager as alive")

	// ── Phase 5: workers rejoin — rejoin loop stops ───────────────────────────

	require.Eventually(t, func() bool {
		return !worker1Svc.rejoining.Load() && !worker2Svc.rejoining.Load()
	}, 3*rejoinInterval, rejoinInterval/10, "rejoin loop should stop once the manager is visible")
}

// TestClusterService_WorkersDoNotStopAtEachOther verifies that the rejoin loop does NOT
// terminate just because workers can contact each other (n > 0 from cluster.Join).
// The loop must only stop when GetMemberByRole(manager) returns non-nil.
func TestClusterService_WorkersDoNotStopAtEachOther(t *testing.T) {
	// Two workers, no manager yet.
	worker1Serf, worker1Addr, _, _ := createSerfNode(t, "worker-1", workerTags())
	_, worker2Addr, _, _ := createSerfNode(t, "worker-2", workerTags())

	// Join the two workers together.
	_, err := worker1Serf.Join([]string{worker2Addr}, true)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return numAliveMembers(worker1Serf) == 2
	}, 3*rejoinInterval, rejoinInterval/10, "workers should see each other")

	// Start the rejoin loop directly on worker-1's service.
	svc1 := &ClusterService{
		runtimeConfiguration: &agent.RuntimeConfig{
			DockerConfig: agent.DockerRuntimeConfig{NodeRole: agent.NodeRoleWorker},
		},
		cluster:        worker1Serf,
		clusterAddr:    "localhost", // DNS resolves but Join finds no Serf node at default port
		rejoinInterval: rejoinInterval,
	}
	svc1.rejoining.Store(true)
	go svc1.rejoinUntilManagerFound()

	// Let several ticker cycles pass — the loop should keep running because
	// GetMemberByRole(manager) is still nil (no manager in the cluster).
	require.Eventually(t, func() bool {
		return numAliveMembers(worker1Serf) == 2
	}, 5*rejoinInterval, rejoinInterval/10, "workers should still see each other")
	require.True(t, svc1.rejoining.Load(), "rejoin loop must still be running: contacting workers is not sufficient to stop it")

	// Now add a manager node and join it to the cluster.
	managerSerf, _, _, _ := createSerfNode(t, "manager", managerTags())

	_, err = managerSerf.Join([]string{worker1Addr}, true)
	require.NoError(t, err)

	// Once the manager is visible as StatusAlive, the loop should exit.
	require.Eventually(t, func() bool {
		return svc1.GetMemberByRole(agent.NodeRoleManager) != nil
	}, 5*rejoinInterval, rejoinInterval/10, "worker-1 should see the manager as alive")

	require.Eventually(t, func() bool {
		return !svc1.rejoining.Load()
	}, 3*rejoinInterval, rejoinInterval/10, "rejoin loop should stop once a manager is visible")
}

// TestClusterService_RejoinLoopContinuesOnDNSFailure verifies that the rejoin loop
// keeps running when DNS lookup returns no addresses (cluster.go:192), and exits
// cleanly once a manager appears in the cluster via Serf (not DNS).
// ".invalid" is an IANA-reserved TLD (RFC 2606) that must never resolve.
func TestClusterService_RejoinLoopContinuesOnDNSFailure(t *testing.T) {
	worker1Serf, worker1Addr, _, _ := createSerfNode(t, "worker-1", workerTags())

	svc := &ClusterService{
		runtimeConfiguration: &agent.RuntimeConfig{
			DockerConfig: agent.DockerRuntimeConfig{NodeRole: agent.NodeRoleWorker},
		},
		cluster:        worker1Serf,
		clusterAddr:    "nonexistent.invalid",
		rejoinInterval: rejoinInterval,
	}
	svc.rejoining.Store(true)
	go svc.rejoinUntilManagerFound()

	assert.Never(t, func() bool {
		return !svc.rejoining.Load()
	}, 5*rejoinInterval, rejoinInterval/10, "rejoin loop must keep running when DNS lookup fails")

	// Add a manager directly to the Serf cluster so the goroutine can reach its exit condition.
	managerSerf, _, _, _ := createSerfNode(t, "manager", managerTags())
	_, err := managerSerf.Join([]string{worker1Addr}, true)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return !svc.rejoining.Load()
	}, 5*rejoinInterval, rejoinInterval/10, "rejoin loop should stop once the manager is visible via Serf")
}
