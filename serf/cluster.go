package serf

import (
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/portainer/agent"
	agentnet "github.com/portainer/agent/net"

	"github.com/hashicorp/logutils"
	"github.com/hashicorp/serf/serf"
	"github.com/rs/zerolog/log"
)

const (
	memberTagKeyAgentPort    = "AgentPort"
	memberTagKeyIsLeader     = "NodeIsLeader"
	memberTagKeyNodeName     = "NodeName"
	memberTagKeyNodeRole     = "DockerNodeRole"
	memberTagKeyEngineStatus = "DockerEngineStatus"
	memberTagKeyEdgeKeySet   = "EdgeKeySet"

	memberTagValueEngineStatusSwarm      = "swarm"
	memberTagValueEngineStatusStandalone = "standalone"
	memberTagValueNodeRoleManager        = "manager"
	memberTagValueNodeRoleWorker         = "worker"
)

// ClusterService is a service used to manage cluster related actions such as joining
// the cluster, retrieving members in the clusters...
type ClusterService struct {
	runtimeConfiguration *agent.RuntimeConfig
	cluster              *serf.Serf
	clusterAddr          string
	rejoining            atomic.Bool
	rejoinInterval       time.Duration // 0 defaults to 10s; override in tests for speed
}

// NewClusterService returns a pointer to a ClusterService.
func NewClusterService(runtimeConfiguration *agent.RuntimeConfig) *ClusterService {
	return &ClusterService{
		runtimeConfiguration: runtimeConfiguration,
	}
}

// NewSwarmClusterService returns a pointer to a ClusterService configured for Docker Swarm.
// The clusterAddr is the DNS name used to re-resolve peers for self-healing rejoin after a manager reap.
func NewSwarmClusterService(runtimeConfiguration *agent.RuntimeConfig, clusterAddr string) *ClusterService {
	return &ClusterService{
		runtimeConfiguration: runtimeConfiguration,
		clusterAddr:          clusterAddr,
	}
}

// Leave leaves the cluster.
func (service *ClusterService) Leave() {
	if service.cluster == nil {
		return
	}

	if err := service.cluster.Leave(); err != nil {
		log.Error().Str("context", "ClusterService").Err(err).Msg("Failed to leave cluster")
	}
}

// Create will create the agent configuration and automatically join the cluster.
func (service *ClusterService) Create(advertiseAddr string, joinAddr []string, probeTimeout, probeInterval time.Duration) error {
	filter := &logutils.LevelFilter{
		Levels:   []logutils.LogLevel{"DEBUG", "INFO", "WARN", "ERROR"},
		MinLevel: logutils.LogLevel("INFO"),
		Writer:   os.Stderr,
	}

	conf := serf.DefaultConfig()
	conf.Init()
	conf.NodeName = fmt.Sprintf("%s-%s", service.runtimeConfiguration.NodeName, conf.NodeName)
	conf.Tags = convertRuntimeConfigurationToTagMap(service.runtimeConfiguration)
	conf.MemberlistConfig.LogOutput = filter
	conf.LogOutput = filter
	conf.MemberlistConfig.AdvertiseAddr = advertiseAddr

	// Only enable event watching on Swarm clusters where self-healing rejoin is needed.
	var eventCh chan serf.Event
	if service.clusterAddr != "" {
		eventCh = make(chan serf.Event, 64)
		conf.EventCh = eventCh
	}

	// These parameters should only be overridden if experiencing agent cluster instability
	// Default memberlist values should work in most clustering use cases but some
	// cluster/network topologies might cause the agent cluster to be unstable and
	// seeing a lot of agent join/leave cluster events.
	// There is no recommended value/range to be set here and instead it is recommended
	// to experiment with different values if facing instability issues.
	conf.MemberlistConfig.ProbeTimeout = probeTimeout
	conf.MemberlistConfig.ProbeInterval = probeInterval

	// Override default Serf configuration with Swarm/overlay sane defaults
	conf.ReconnectInterval = 10 * time.Second
	conf.ReconnectTimeout = 1 * time.Minute

	log.Debug().
		Str("context", "ClusterService").
		Str("advertise_address", advertiseAddr).
		Strs("join_address", joinAddr).
		Msg("Creating cluster")

	cluster, err := serf.Create(conf)
	if err != nil {
		return err
	}

	service.cluster = cluster

	// Must be started before Join() so the goroutine drains EventMemberJoin events in real
	// time — Serf sends to EventCh with a blocking send (no select/default).
	if eventCh != nil {
		go service.watchClusterEvents(eventCh)
	}

	nodeCount, err := cluster.Join(joinAddr, true)
	if err != nil {
		// Join failure is non-fatal: best-effort startup. When clusterAddr is set
		// (Swarm), the self-healing rejoin loop will recover. Warn only when a
		// join address was provided so operators can detect a degraded initial state.
		if len(joinAddr) > 0 {
			log.Warn().
				Str("context", "ClusterService").
				Strs("join_addr", joinAddr).
				Err(err).
				Msg("Initial cluster join failed; starting in degraded state")
		}
	}

	log.Debug().
		Str("context", "ClusterService").
		Int("contacted_nodes", nodeCount).
		Msg("Cluster join attempted")

	return nil
}

// watchClusterEvents listens for Serf events and triggers a cluster rejoin
// when a manager agent is reaped.
func (service *ClusterService) watchClusterEvents(eventCh <-chan serf.Event) {
	for event := range eventCh {
		memberEvent, ok := event.(serf.MemberEvent)
		if !ok || memberEvent.Type != serf.EventMemberReap {
			continue
		}

		for _, member := range memberEvent.Members {
			if member.Tags[memberTagKeyNodeRole] != memberTagValueNodeRoleManager {
				continue
			}

			if service.rejoining.CompareAndSwap(false, true) {
				log.Debug().
					Str("context", "ClusterService").
					Str("cluster_addr", service.clusterAddr).
					Str("member", member.Name).
					Msg("Manager agent reaped, starting rejoin loop")
				go service.rejoinUntilManagerFound()
			}
			break
		}
	}
}

// rejoinUntilManagerFound periodically re-resolves the cluster DNS address and attempts
// to join until a manager node appears in the Serf member list. This recovers from the
// race between EventMemberReap and the new manager container registering in DNS.
func (service *ClusterService) rejoinUntilManagerFound() {
	defer service.rejoining.Store(false)

	interval := service.rejoinInterval
	if interval == 0 {
		interval = 10 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		if service.GetMemberByRole(agent.NodeRoleManager) != nil {
			return
		}

		addrs, err := agentnet.LookupIPAddresses(service.clusterAddr)
		if err != nil || len(addrs) == 0 {
			log.Debug().
				Str("context", "ClusterService").
				Str("cluster_addr", service.clusterAddr).
				Err(err).
				Msg("Cluster rejoin: DNS lookup returned no results")
			continue
		}

		n, err := service.cluster.Join(addrs, true)
		if err != nil || n == 0 {
			log.Debug().
				Str("context", "ClusterService").
				Str("cluster_addr", service.clusterAddr).
				Err(err).
				Msg("Cluster rejoin: unable to contact peers")
			continue
		}

		log.Debug().
			Str("context", "ClusterService").
			Str("cluster_addr", service.clusterAddr).
			Int("contacted_nodes", n).
			Msg("Cluster rejoin: join attempted")
	}
}

// Members returns the list of cluster members.
func (service *ClusterService) Members() []agent.ClusterMember {
	var clusterMembers = make([]agent.ClusterMember, 0)

	members := service.cluster.Members()

	for _, member := range members {
		if member.Status == serf.StatusAlive {
			clusterMember := agent.ClusterMember{
				IPAddress:  member.Addr.String(),
				Port:       member.Tags[memberTagKeyAgentPort],
				NodeRole:   member.Tags[memberTagKeyNodeRole],
				NodeName:   member.Tags[memberTagKeyNodeName],
				EdgeKeySet: false,
			}

			if _, ok := member.Tags[memberTagKeyEdgeKeySet]; ok {
				clusterMember.EdgeKeySet = true
			}

			clusterMembers = append(clusterMembers, clusterMember)
		}
	}

	return clusterMembers
}

// GetMemberByRole will return the first member with the specified role.
func (service *ClusterService) GetMemberByRole(role agent.DockerNodeRole) *agent.ClusterMember {
	members := service.Members()

	roleString := memberTagValueNodeRoleManager
	if role == agent.NodeRoleWorker {
		roleString = memberTagValueNodeRoleWorker
	}

	for _, member := range members {
		if member.NodeRole == roleString {
			return &member
		}
	}

	return nil
}

// GetMemberByNodeName will return the first member with the specified node name.
func (service *ClusterService) GetMemberByNodeName(nodeName string) *agent.ClusterMember {
	members := service.Members()
	for _, member := range members {
		if member.NodeName == nodeName {
			return &member
		}
	}

	return nil
}

// GetMemberWithEdgeKeySet will return the first member with the EdgeKeySet tag set.
func (service *ClusterService) GetMemberWithEdgeKeySet() *agent.ClusterMember {
	members := service.Members()
	for _, member := range members {
		if member.EdgeKeySet {
			return &member
		}
	}

	return nil
}

// UpdateRuntimeConfiguration propagate the new runtimeConfiguration to the cluster
func (service *ClusterService) UpdateRuntimeConfiguration(runtimeConfiguration *agent.RuntimeConfig) error {
	service.runtimeConfiguration = runtimeConfiguration
	tagsMap := convertRuntimeConfigurationToTagMap(runtimeConfiguration)

	return service.cluster.SetTags(tagsMap)
}

// GetRuntimeConfiguration returns the runtimeConfiguration associated to the service
func (service *ClusterService) GetRuntimeConfiguration() *agent.RuntimeConfig {
	return service.runtimeConfiguration
}

func convertRuntimeConfigurationToTagMap(runtimeConfiguration *agent.RuntimeConfig) map[string]string {
	tagsMap := map[string]string{}

	if runtimeConfiguration.EdgeKeySet {
		tagsMap[memberTagKeyEdgeKeySet] = "set"
	}

	tagsMap[memberTagKeyEngineStatus] = memberTagValueEngineStatusStandalone
	if runtimeConfiguration.DockerConfig.EngineType == agent.EngineTypeSwarm {
		tagsMap[memberTagKeyEngineStatus] = memberTagValueEngineStatusSwarm
	}

	tagsMap[memberTagKeyAgentPort] = runtimeConfiguration.AgentPort

	if runtimeConfiguration.DockerConfig.Leader {
		tagsMap[memberTagKeyIsLeader] = "1"
	}

	tagsMap[memberTagKeyNodeName] = runtimeConfiguration.NodeName

	tagsMap[memberTagKeyNodeRole] = memberTagValueNodeRoleManager
	if runtimeConfiguration.DockerConfig.NodeRole == agent.NodeRoleWorker {
		tagsMap[memberTagKeyNodeRole] = memberTagValueNodeRoleWorker
	}

	return tagsMap
}
