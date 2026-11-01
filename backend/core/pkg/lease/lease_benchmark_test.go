package lease

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
)

func BenchmarkAcquireAndRelease(b *testing.B) {
	ctx := context.Background()
	mgr := NewInMemoryLeaseManager()
	mgr.SetInitialInventory("12301", "20261105", "3A", "GN", 10000000)

	var userCounter int64

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			uid := fmt.Sprintf("user-%d", atomic.AddInt64(&userCounter, 1))
			req := AcquireLeaseRequest{
				UserID:         uid,
				TrainNumber:    "12301",
				RunDate:        "20261105",
				ClassCode:      "3A",
				QuotaCode:      "GN",
				PassengerCount: 1,
				TTLSeconds:     300,
			}
			res, err := mgr.AcquireLease(ctx, req)
			if err != nil || !res.Success {
				b.Fatalf("acquire error: %v", err)
			}
			// Voluntary release
			_ = mgr.ReleaseLease(ctx, "12301", "20261105", res.LeaseToken, uid)
		}
	})
}
