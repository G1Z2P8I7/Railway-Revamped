package resilience

import (
	"fmt"
	"testing"
	"time"
)

func TestMultiRegionActiveActiveFailoverAndZeroRPO(t *testing.T) {
	cluster := NewMultiRegionQuorumCluster()

	// 1. Commit initial transactions under normal 4-region health
	var committedTxnIDs []string
	for i := 0; i < 50; i++ {
		txn := TransactionRecord{
			TxnID:     GenerateTxnID(),
			PNR:       fmt.Sprintf("PNR-%d", 1000+i),
			BerthKey:  fmt.Sprintf("B1-%d", i+1),
			Segments:  0x000000000000000F,
			Timestamp: time.Now(),
			Region:    RegionNorthDelhi,
		}
		ok, lat, err := cluster.ProposeCommit(txn)
		if err != nil || !ok {
			t.Fatalf("commit failed at %d: %v", i, err)
		}
		if lat > 300*time.Millisecond {
			t.Fatalf("latency exceeded 300ms budget: %v", lat)
		}
		committedTxnIDs = append(committedTxnIDs, txn.TxnID)
	}

	t.Logf("Initial 50 transactions committed across 4 regions with consensus")

	// 2. Inject Catastrophic Disaster: Total failure of RegionNorthDelhi (active leader)
	rto, err := cluster.SimulateRegionalOutage(RegionNorthDelhi)
	if err != nil {
		t.Fatalf("unexpected failover error: %v", err)
	}

	// Verify RTO <= 60 seconds (in reality < 10ms for automated leader promotion)
	if rto > 60*time.Second {
		t.Fatalf("RTO exceeded 60 second SLA: %v", rto)
	}
	t.Logf("Regional Outage Simulated: Delhi down. Failover RTO = %v (Target <= 60s)", rto)

	// 3. Verify Zero Data Loss (RPO = 0s) across surviving 3 nodes (Mumbai, Bengaluru, Kolkata)
	rpoZero, missing := cluster.VerifyZeroDataLoss(committedTxnIDs)
	if !rpoZero || missing > 0 {
		t.Fatalf("RPO violation: %d commits missing after regional failure!", missing)
	}
	t.Logf("RPO = 0s Verified: 100%% of committed transactions preserved across surviving quorum")

	// 4. Continue writing during outage: surviving 3 nodes maintain quorum (3 of 4)
	for i := 50; i < 100; i++ {
		txn := TransactionRecord{
			TxnID:     GenerateTxnID(),
			PNR:       fmt.Sprintf("PNR-%d", 1000+i),
			BerthKey:  fmt.Sprintf("B2-%d", i+1),
			Segments:  0x000000000000000F,
			Timestamp: time.Now(),
			Region:    RegionWestMumbai,
		}
		ok, _, err := cluster.ProposeCommit(txn)
		if err != nil || !ok {
			t.Fatalf("degraded quorum commit failed at %d: %v", i, err)
		}
		committedTxnIDs = append(committedTxnIDs, txn.TxnID)
	}
	t.Logf("Post-failure transactions (50..99) successfully committed on 3-node surviving quorum")

	// 5. Recover RegionNorthDelhi and sync missed transactions
	synced, err := cluster.RecoverRegion(RegionNorthDelhi)
	if err != nil {
		t.Fatalf("region recovery failed: %v", err)
	}
	if synced != 50 {
		t.Fatalf("expected 50 synced transactions for recovering node, got %d", synced)
	}
	t.Logf("RegionNorthDelhi fully recovered and synced %d missing transactions from cluster leader", synced)

	// 6. Verify cluster-wide consistency across all 4 regions
	rpoZero, missing = cluster.VerifyZeroDataLoss(committedTxnIDs)
	if !rpoZero || missing > 0 {
		t.Fatalf("consistency check failed after recovery: %d missing", missing)
	}
	t.Logf("Full Cluster Consistency Confirmed: All 100 transactions synchronized across all 4 regions")
}
