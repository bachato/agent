package serf

import (
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/hashicorp/serf/serf"
	"github.com/portainer/agent"
	"github.com/stretchr/testify/require"
)

// rejoinInterval controls all timeouts in this test via multiples.
// In production ClusterService this is hardcoded to 10s; here we use a small
// value to keep the test fast. Increase it if the test becomes flaky on slow CI.
const rejoinInterval = 100 * time.Millisecond

// noopWriter discards all log output.
type noopWriter struct{}

func (noopWriter) Write(p []byte) (int, error) { return len(p), nil }

// freeListener binds :0 and returns both the address and the open listener.
// The caller should close the listener immediately before binding the port with
// another socket to minimise the TOCTOU race window.
func freeListener(t *testing.T) (string, net.Listener) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	return l.Addr().String(), l
}

// testSerfConfig returns a serf.Config tuned for fast convergence in unit tests.
// addr must be "ip:port". tags are applied verbatim to the node.
func testSerfConfig(t *testing.T, nodeName, addr string, tags map[string]string) *serf.Config {
	t.Helper()

	host, portStr, err := net.SplitHostPort(addr)
	require.NoError(t, err)

	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)
	require.NotZero(t, port)

	conf := serf.DefaultConfig()
	conf.Init()
	conf.NodeName = nodeName
	conf.Tags = tags
	conf.MemberlistConfig.BindAddr = host
	conf.MemberlistConfig.BindPort = port
	conf.MemberlistConfig.AdvertiseAddr = host
	conf.MemberlistConfig.AdvertisePort = port

	// Aggressive timings for fast test convergence.
	conf.MemberlistConfig.GossipInterval = 5 * time.Millisecond
	conf.MemberlistConfig.ProbeInterval = 50 * time.Millisecond
	conf.MemberlistConfig.ProbeTimeout = 25 * time.Millisecond
	conf.MemberlistConfig.TCPTimeout = 200 * time.Millisecond
	conf.MemberlistConfig.SuspicionMult = 1

	conf.ReconnectInterval = 100 * time.Millisecond
	conf.ReconnectTimeout = time.Microsecond
	conf.TombstoneTimeout = time.Microsecond
	conf.ReapInterval = 300 * time.Millisecond

	// Suppress Serf/memberlist log noise in test output.
	conf.LogOutput = noopWriter{}
	conf.MemberlistConfig.LogOutput = noopWriter{}

	return conf
}

// createSerfNodeAt creates a Serf node at a fixed address and returns the serf instance,
// the address, an event channel, and a cancel func that shuts the node down (safe to call multiple times).
// t.Cleanup also calls cancel as a fallback, so callers that need early shutdown can call cancel()
// without risking a double-shutdown.
func createSerfNodeAt(t *testing.T, name, addr string, tags map[string]string) (s *serf.Serf, nodeAddr string, eventCh <-chan serf.Event, cancel func()) {
	t.Helper()
	nodeAddr = addr
	nodeConf := testSerfConfig(t, name, addr, tags)
	ch := make(chan serf.Event, 64)
	nodeConf.EventCh = ch
	eventCh = ch
	var err error
	s, err = serf.Create(nodeConf)
	require.NoError(t, err)
	cancel = func() { s.Shutdown() } //nolint:errcheck
	t.Cleanup(cancel)
	return
}

// createSerfNode creates a Serf node at a randomly chosen free address.
// It holds the listener open until just before serf.Create to minimise the
// TOCTOU window between port allocation and Serf binding.
func createSerfNode(t *testing.T, name string, tags map[string]string) (*serf.Serf, string, <-chan serf.Event, func()) {
	t.Helper()
	addr, l := freeListener(t)
	nodeConf := testSerfConfig(t, name, addr, tags)
	ch := make(chan serf.Event, 64)
	nodeConf.EventCh = ch
	_ = l.Close() // release port immediately before Serf binds it
	s, err := serf.Create(nodeConf)
	require.NoError(t, err)
	cancel := func() { s.Shutdown() } //nolint:errcheck
	t.Cleanup(cancel)
	return s, addr, ch, cancel
}

// newWorkerService creates a ClusterService for a worker node backed by an already-running
// serf.Serf instance. clusterAddr is set so that rejoinUntilManagerFound is activated.
// interval overrides the default 10 s ticker for test speed.
// eventCh must be the same channel passed to serf.Config.EventCh when the node was created.
func newWorkerService(t *testing.T, s *serf.Serf, eventCh <-chan serf.Event, clusterAddr string, interval time.Duration) *ClusterService {
	t.Helper()

	svc := NewSwarmClusterService(&agent.RuntimeConfig{
		DockerConfig: agent.DockerRuntimeConfig{
			NodeRole: agent.NodeRoleWorker,
		},
	}, clusterAddr)
	svc.cluster = s
	svc.rejoinInterval = interval

	if eventCh != nil {
		go svc.watchClusterEvents(eventCh)
	}

	return svc
}

// newManagerService creates a ClusterService for a manager node backed by an
// already-running serf.Serf instance. Used by member-lookup tests.
func newManagerService(s *serf.Serf) *ClusterService {
	svc := NewSwarmClusterService(&agent.RuntimeConfig{
		NodeName: "manager",
		DockerConfig: agent.DockerRuntimeConfig{
			NodeRole: agent.NodeRoleManager,
		},
	}, "")
	svc.cluster = s
	return svc
}

// numAliveMembers counts members with StatusAlive in the raw serf member list.
func numAliveMembers(s *serf.Serf) int {
	count := 0
	for _, m := range s.Members() {
		if m.Status == serf.StatusAlive {
			count++
		}
	}
	return count
}

// managerTags returns the tag map for a manager node.
func managerTags() map[string]string {
	return map[string]string{
		memberTagKeyNodeRole: memberTagValueNodeRoleManager,
	}
}

// workerTags returns the tag map for a worker node.
func workerTags() map[string]string {
	return map[string]string{
		memberTagKeyNodeRole: memberTagValueNodeRoleWorker,
	}
}
