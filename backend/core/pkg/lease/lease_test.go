package lease

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLeaseAcquireAndRelease(t *testing.T) {
	ctx := context.Background()
	mgr := NewInMemoryLeaseManager()
	mgr.SetInitialInventory("12301", "20261105", "3A", "GN", 10)

	// 1. Acquire lease for 2 passengers
	req := AcquireLeaseRequest{
		UserID:         "user-101",
		TrainNumber:    "12301",
		RunDate:        "20261105",
		ClassCode:      "3A",
		QuotaCode:      "GN",
		OriginStopIdx:  0,
		DestStopIdx:    2,
		SegmentMask:    0b0011,
		PassengerCount: 2,
		TTLSeconds:     300,
	}

	res, err := mgr.AcquireLease(ctx, req)
	if err != nil {
		t.Fatalf("unexpected acquire error: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success")
	}
	if res.RemainingInventory != 8 {
		t.Fatalf("expected remaining inventory 8, got: %d", res.RemainingInventory)
	}

	// 2. User attempts second concurrent lease -> should be blocked by Per-User Cap
	req2 := req
	req2.PassengerCount = 1
	_, err = mgr.AcquireLease(ctx, req2)
	if err != ErrActiveLeaseExists {
		t.Fatalf("expected ErrActiveLeaseExists, got: %v", err)
	}

	// 3. User voluntarily releases lease
	err = mgr.ReleaseLease(ctx, "12301", "20261105", res.LeaseToken, "user-101")
	if err != nil {
		t.Fatalf("unexpected release error: %v", err)
	}

	// Inventory should be restored to 10
	avail := mgr.GetAvailableInventory("12301", "20261105", "3A", "GN")
	if avail != 10 {
		t.Fatalf("expected restored inventory 10, got: %d", avail)
	}

	// User should now be able to acquire again
	res3, err := mgr.AcquireLease(ctx, req)
	if err != nil {
		t.Fatalf("user should be able to acquire after release, got: %v", err)
	}
	if res3.RemainingInventory != 8 {
		t.Fatalf("expected 8 remaining, got: %d", res3.RemainingInventory)
	}
}

func TestLeaseSweeperReclamation(t *testing.T) {
	ctx := context.Background()
	mgr := NewInMemoryLeaseManager()
	mgr.SetInitialInventory("12301", "20261105", "3A", "GN", 5)

	// Acquire short lease (TTL = 1s)
	req := AcquireLeaseRequest{
		UserID:         "user-fast-expire",
		TrainNumber:    "12301",
		RunDate:        "20261105",
		ClassCode:      "3A",
		QuotaCode:      "GN",
		PassengerCount: 3,
		TTLSeconds:     1,
	}

	res, err := mgr.AcquireLease(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if mgr.GetAvailableInventory("12301", "20261105", "3A", "GN") != 2 {
		t.Fatalf("inventory should be 2")
	}

	// Sweeping immediately should reclaim 0
	now := time.Now().Unix()
	swept, _ := mgr.SweepExpiredLeases(ctx, "12301", "20261105", now)
	if swept != 0 {
		t.Fatalf("expected 0 swept immediately, got: %d", swept)
	}

	// Simulate time passing (now + 2 seconds)
	futureNow := now + 2
	swept, err = mgr.SweepExpiredLeases(ctx, "12301", "20261105", futureNow)
	if err != nil {
		t.Fatal(err)
	}
	if swept != 1 {
		t.Fatalf("expected 1 lease swept, got: %d", swept)
	}

	// Inventory must be fully restored to 5 (Zero Seat Leaks!)
	if mgr.GetAvailableInventory("12301", "20261105", "3A", "GN") != 5 {
		t.Fatalf("inventory must be restored to 5 after sweep")
	}

	// Lease should no longer be retrievable
	_, err = mgr.GetLease(ctx, "12301", "20261105", res.LeaseToken)
	if err != ErrLeaseNotFound {
		t.Fatalf("expected ErrLeaseNotFound, got: %v", err)
	}
}

func TestHighConcurrencyNoOverAllocation(t *testing.T) {
	ctx := context.Background()
	mgr := NewInMemoryLeaseManager()

	initialSeats := 100
	mgr.SetInitialInventory("12301", "20261105", "3A", "GN", initialSeats)

	// 500 concurrent users racing to lease 2 seats each (demand: 1,000 seats vs supply: 100 seats)
	concurrentUsers := 500
	var wg sync.WaitGroup
	var successfulLeases int64
	var failedLeases int64

	wg.Add(concurrentUsers)
	for i := 0; i < concurrentUsers; i++ {
		userID := string(rune('A'+(i%26))) + string(rune('0'+(i/26))) + time.Now().Format("150405.000000000")
		go func(uid string) {
			defer wg.Done()
			req := AcquireLeaseRequest{
				UserID:         uid,
				TrainNumber:    "12301",
				RunDate:        "20261105",
				ClassCode:      "3A",
				QuotaCode:      "GN",
				PassengerCount: 2,
				TTLSeconds:     300,
			}
			res, err := mgr.AcquireLease(ctx, req)
			if err == nil && res.Success {
				atomic.AddInt64(&successfulLeases, 1)
			} else {
				atomic.AddInt64(&failedLeases, 1)
			}
		}(userID)
	}

	wg.Wait()

	// Exactly 50 leases (50 * 2 = 100 seats) must succeed
	if successfulLeases != 50 {
		t.Fatalf("expected exactly 50 successful leases, got: %d", successfulLeases)
	}
	if failedLeases != int64(concurrentUsers-50) {
		t.Fatalf("expected %d failed leases, got: %d", concurrentUsers-50, failedLeases)
	}

	// Remaining inventory must be exactly 0 (no negative values, no race condition corruption)
	remaining := mgr.GetAvailableInventory("12301", "20261105", "3A", "GN")
	if remaining != 0 {
		t.Fatalf("expected remaining inventory 0, got: %d", remaining)
	}
}

func TestCommitLeasePostPayment(t *testing.T) {
	ctx := context.Background()
	mgr := NewInMemoryLeaseManager()
	mgr.SetInitialInventory("12301", "20261105", "3A", "GN", 10)

	req := AcquireLeaseRequest{
		UserID:         "user-buyer",
		TrainNumber:    "12301",
		RunDate:        "20261105",
		ClassCode:      "3A",
		QuotaCode:      "GN",
		PassengerCount: 2,
		TTLSeconds:     300,
	}

	res, err := mgr.AcquireLease(ctx, req)
	if err != nil {
		t.Fatal(err)
	}

	// Payment succeeds -> Commit Lease
	err = mgr.CommitLease(ctx, "12301", "20261105", res.LeaseToken)
	if err != nil {
		t.Fatalf("unexpected commit error: %v", err)
	}

	// Inventory must remain 8 (seat is booked!)
	if mgr.GetAvailableInventory("12301", "20261105", "3A", "GN") != 8 {
		t.Fatalf("expected inventory to stay 8")
	}

	// User lock should be released so they can book another journey later
	reqNew := req
	reqNew.PassengerCount = 1
	res2, err := mgr.AcquireLease(ctx, reqNew)
	if err != nil {
		t.Fatalf("user should be unlocked post-commit, got: %v", err)
	}
	if !res2.Success {
		t.Fatalf("expected success")
	}
}
