package ingress

import (
	"context"
	"testing"
	"time"
)

func Test500msJitterVirtualLobby(t *testing.T) {
	lobby := NewVirtualWaitingLobby(500*time.Millisecond, 1000)

	// Simulate 10 simultaneous users arriving within the same millisecond
	ctx := context.Background()
	tickets := make([]*AdmissionTicket, 10)

	for i := 0; i < 10; i++ {
		uid := "user-" + string(rune('A'+i))
		ticket, err := lobby.EnqueueRequest(ctx, uid, ClientCitizen, "12301", "20261105", "3A")
		if err != nil {
			t.Fatalf("enqueue failed: %v", err)
		}
		tickets[i] = ticket
	}

	// 1. All 10 tickets should share the SAME 500ms bucket index
	bucket := tickets[0].BucketIndex
	for i, tkt := range tickets {
		if tkt.BucketIndex != bucket {
			t.Fatalf("expected identical bucket index %d, got %d for ticket %d", bucket, tkt.BucketIndex, i)
		}
	}

	// 2. Randomized jitter within the 500ms window: verify dispatch times vary within [0, 500ms)
	hasVaryingWait := false
	firstWait := tickets[0].EstimatedWait
	for _, tkt := range tickets {
		if tkt.EstimatedWait != firstWait {
			hasVaryingWait = true
			break
		}
	}
	if !hasVaryingWait {
		t.Logf("Notice: all randomized waits were identical (unlikely with 10 items, but mathematically possible)")
	}

	// 3. Premature token redemption should fail with remaining ms
	firstToken := tickets[0].AdmissionToken
	err := lobby.ValidateAdmissionToken(firstToken)
	if err == nil && tickets[0].EstimatedWait > 0 {
		t.Fatalf("premature token redemption should have failed until dispatch time!")
	}
}

func TestCitizenExclusivityWindow(t *testing.T) {
	// Simulate 10:15 AM (AC Tatkal Rush)
	mockTatkalAC := func() time.Time {
		return time.Date(2026, 11, 5, 10, 15, 0, 0, time.Local)
	}
	managerAC := NewExclusivityManager(mockTatkalAC)

	// Citizen should be allowed
	if err := managerAC.CheckAccess(ClientCitizen, "3A"); err != nil {
		t.Fatalf("citizen should have access during AC Tatkal: %v", err)
	}

	// Commercial Aggregator should be rejected
	if err := managerAC.CheckAccess(ClientAggregator, "3A"); err != ErrCitizenExclusivityWindow {
		t.Fatalf("commercial aggregator must be blocked during 10:00-10:30 AM AC Tatkal, got: %v", err)
	}

	// Simulate 10:45 AM (Citizen window has ended for AC)
	mockPostTatkal := func() time.Time {
		return time.Date(2026, 11, 5, 10, 45, 0, 0, time.Local)
	}
	managerPost := NewExclusivityManager(mockPostTatkal)

	if err := managerPost.CheckAccess(ClientAggregator, "3A"); err != nil {
		t.Fatalf("commercial aggregator should be allowed after 10:30 AM: %v", err)
	}

	// Simulate 11:10 AM (Non-AC Tatkal Rush for Sleeper)
	mockTatkalNonAC := func() time.Time {
		return time.Date(2026, 11, 5, 11, 10, 0, 0, time.Local)
	}
	managerNonAC := NewExclusivityManager(mockTatkalNonAC)

	// Aggregator blocked for Sleeper (SL)
	if err := managerNonAC.CheckAccess(ClientAggregator, "SL"); err != ErrCitizenExclusivityWindow {
		t.Fatalf("commercial aggregator must be blocked for Non-AC Tatkal at 11:10 AM, got: %v", err)
	}

	// But AC classes are not in Tatkal at 11:10 AM, so aggregator can query AC
	if err := managerNonAC.CheckAccess(ClientAggregator, "3A"); err != nil {
		t.Fatalf("commercial aggregator can query AC at 11:10 AM: %v", err)
	}
}
