package resilience

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// RegionID represents sovereign MeitY-empanelled domestic cloud regions
type RegionID string

const (
	RegionNorthDelhi    RegionID = "REGION_1_NORTH_DELHI"
	RegionWestMumbai    RegionID = "REGION_2_WEST_MUMBAI"
	RegionSouthBengaluru RegionID = "REGION_3_SOUTH_BENGALURU"
	RegionEastKolkata   RegionID = "REGION_4_EAST_KOLKATA"
)

// TransactionRecord represents an atomic distributed booking commit
type TransactionRecord struct {
	TxnID     string    `json:"txn_id"`
	PNR       string    `json:"pnr"`
	BerthKey  string    `json:"berth_key"`
	Segments  uint64    `json:"segments"`
	Timestamp time.Time `json:"timestamp"`
	Region    RegionID  `json:"origin_region"`
}

// ClusterNode models a distributed SQL database replica in a domestic data center
type ClusterNode struct {
	mu           sync.RWMutex
	NodeID       string
	Region       RegionID
	IsHealthy    bool
	IsLeader     bool
	CommittedLog map[string]TransactionRecord
}

// MultiRegionQuorumCluster models the 4-region active-active distributed core (CockroachDB/Yugabyte)
type MultiRegionQuorumCluster struct {
	mu          sync.RWMutex
	nodes       map[string]*ClusterNode
	leaderID    string
	quorumSize  int // Quorum = (N/2)+1 = 3 out of 4 regions
	failoverRTO time.Duration
}

// NewMultiRegionQuorumCluster initializes the 4-region nationwide active-active cluster
func NewMultiRegionQuorumCluster() *MultiRegionQuorumCluster {
	cluster := &MultiRegionQuorumCluster{
		nodes:      make(map[string]*ClusterNode),
		quorumSize: 3, // 3 of 4 required for strict write consensus
	}

	regions := []RegionID{
		RegionNorthDelhi,
		RegionWestMumbai,
		RegionSouthBengaluru,
		RegionEastKolkata,
	}

	for i, reg := range regions {
		nodeID := fmt.Sprintf("node-%s-%d", reg, i+1)
		node := &ClusterNode{
			NodeID:       nodeID,
			Region:       reg,
			IsHealthy:    true,
			IsLeader:     (i == 0), // Delhi starts as primary coordinator
			CommittedLog: make(map[string]TransactionRecord),
		}
		cluster.nodes[nodeID] = node
		if node.IsLeader {
			cluster.leaderID = nodeID
		}
	}

	return cluster
}

// ProposeCommit broadcasts an atomic reservation commit across all 4 regions requiring 3-node quorum
func (c *MultiRegionQuorumCluster) ProposeCommit(record TransactionRecord) (bool, time.Duration, error) {
	start := time.Now()
	c.mu.RLock()
	defer c.mu.RUnlock()

	var healthyNodes []*ClusterNode
	for _, n := range c.nodes {
		n.mu.RLock()
		if n.IsHealthy {
			healthyNodes = append(healthyNodes, n)
		}
		n.mu.RUnlock()
	}

	if len(healthyNodes) < c.quorumSize {
		return false, time.Since(start), fmt.Errorf("quorum lost: only %d nodes healthy, %d required", len(healthyNodes), c.quorumSize)
	}

	// Replicate synchronously to quorum
	ackCount := 0
	for _, n := range healthyNodes {
		n.mu.Lock()
		n.CommittedLog[record.TxnID] = record
		n.mu.Unlock()
		ackCount++
	}

	latency := time.Since(start)
	return (ackCount >= c.quorumSize), latency, nil
}

// SimulateRegionalOutage simulates the sudden catastrophic loss of an entire regional data center
func (c *MultiRegionQuorumCluster) SimulateRegionalOutage(region RegionID) (time.Duration, error) {
	failoverStart := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()

	lostLeader := false
	for _, n := range c.nodes {
		if n.Region == region {
			n.mu.Lock()
			n.IsHealthy = false
			if n.IsLeader {
				n.IsLeader = false
				lostLeader = true
			}
			n.mu.Unlock()
		}
	}

	// If the leader was in the failed region, elect a new coordinator among surviving healthy nodes
	if lostLeader {
		var survivingHealthy []*ClusterNode
		for _, n := range c.nodes {
			n.mu.RLock()
			if n.IsHealthy {
				survivingHealthy = append(survivingHealthy, n)
			}
			n.mu.RUnlock()
		}

		if len(survivingHealthy) < c.quorumSize {
			return 0, fmt.Errorf("cluster partitioned: surviving nodes (%d) below quorum (%d)", len(survivingHealthy), c.quorumSize)
		}

		// Promote first surviving node to leader
		survivingHealthy[0].mu.Lock()
		survivingHealthy[0].IsLeader = true
		c.leaderID = survivingHealthy[0].NodeID
		survivingHealthy[0].mu.Unlock()
	}

	c.failoverRTO = time.Since(failoverStart)
	return c.failoverRTO, nil
}

// RecoverRegion brings an offline region back up and synchronizes missing log entries
func (c *MultiRegionQuorumCluster) RecoverRegion(region RegionID) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Get latest master log from active leader
	leader, exists := c.nodes[c.leaderID]
	if !exists || !leader.IsHealthy {
		return 0, fmt.Errorf("no healthy leader available for sync")
	}

	leader.mu.RLock()
	masterLog := make(map[string]TransactionRecord)
	for k, v := range leader.CommittedLog {
		masterLog[k] = v
	}
	leader.mu.RUnlock()

	syncedCount := 0
	for _, n := range c.nodes {
		if n.Region == region {
			n.mu.Lock()
			n.IsHealthy = true
			// Catch up missing commits
			for k, v := range masterLog {
				if _, ok := n.CommittedLog[k]; !ok {
					n.CommittedLog[k] = v
					syncedCount++
				}
			}
			n.mu.Unlock()
		}
	}

	return syncedCount, nil
}

// VerifyZeroDataLoss (RPO = 0) verifies that every transaction acknowledged prior to failure is intact
func (c *MultiRegionQuorumCluster) VerifyZeroDataLoss(expectedTxnIDs []string) (rpoZero bool, missingCount int) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, n := range c.nodes {
		n.mu.RLock()
		if n.IsHealthy {
			for _, txnID := range expectedTxnIDs {
				if _, ok := n.CommittedLog[txnID]; !ok {
					missingCount++
				}
			}
		}
		n.mu.RUnlock()
	}

	return (missingCount == 0), missingCount
}

// GetFailoverRTO returns the measured Recovery Time Objective
func (c *MultiRegionQuorumCluster) GetFailoverRTO() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.failoverRTO
}

func GenerateTxnID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return "txn_" + hex.EncodeToString(b)
}
