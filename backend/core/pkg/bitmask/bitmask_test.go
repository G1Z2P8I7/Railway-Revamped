package bitmask

import (
	"testing"
)

func TestSegmentMask(t *testing.T) {
	tests := []struct {
		name        string
		origin      int
		dest        int
		expected    uint64
		expectError bool
	}{
		{
			name:        "NDLS to PRYJ (0 to 2) -> legs 0,1 -> 0b0011 (3)",
			origin:      0,
			dest:        2,
			expected:    0b0011,
			expectError: false,
		},
		{
			name:        "PRYJ to HWH (2 to 4) -> legs 2,3 -> 0b1100 (12)",
			origin:      2,
			dest:        4,
			expected:    0b1100,
			expectError: false,
		},
		{
			name:        "Single hop (1 to 2) -> leg 1 -> 0b0010 (2)",
			origin:      1,
			dest:        2,
			expected:    0b0010,
			expectError: false,
		},
		{
			name:        "Origin equals Dest",
			origin:      2,
			dest:        2,
			expected:    0,
			expectError: true,
		},
		{
			name:        "Negative Origin",
			origin:      -1,
			dest:        3,
			expected:    0,
			expectError: true,
		},
		{
			name:        "Origin greater than Dest",
			origin:      5,
			dest:        3,
			expected:    0,
			expectError: true,
		},
		{
			name:        "Max 64 stops boundary",
			origin:      63,
			dest:        64,
			expected:    uint64(1) << 63,
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mask, err := SegmentMask(tt.origin, tt.dest)
			if (err != nil) != tt.expectError {
				t.Fatalf("expected error: %v, got: %v", tt.expectError, err)
			}
			if mask != tt.expected {
				t.Fatalf("expected mask: %b (%d), got: %b (%d)", tt.expected, tt.expected, mask, mask)
			}
		})
	}
}

func TestSharedBerthSharing(t *testing.T) {
	// Spec validation:
	// Passenger 1 (NDLS -> PRYJ, stops 0->2): Mask = 0b0011
	// Passenger 2 (PRYJ -> HWH, stops 2->4): Mask = 0b1100
	mask1, err := SegmentMask(0, 2)
	if err != nil {
		t.Fatal(err)
	}
	mask2, err := SegmentMask(2, 4)
	if err != nil {
		t.Fatal(err)
	}

	if Overlaps(mask1, mask2) {
		t.Fatalf("expected masks %b and %b to NOT overlap", mask1, mask2)
	}

	// Passenger 1 allocates berth
	occupancy := uint64(0)
	occupancy, err = Allocate(occupancy, mask1)
	if err != nil {
		t.Fatal(err)
	}
	if occupancy != 0b0011 {
		t.Fatalf("unexpected occupancy: %b", occupancy)
	}

	// Passenger 2 can also allocate the SAME physical berth!
	if !IsAvailable(occupancy, mask2) {
		t.Fatalf("berth should be available for Passenger 2 on legs 2,3")
	}

	occupancy, err = Allocate(occupancy, mask2)
	if err != nil {
		t.Fatal(err)
	}
	if occupancy != 0b1111 {
		t.Fatalf("expected both passengers to occupy 0b1111, got: %b", occupancy)
	}

	// Passenger 3 tries CNB -> DDU (stops 1->3): Mask = 0b0110
	mask3, _ := SegmentMask(1, 3)
	if IsAvailable(occupancy, mask3) {
		t.Fatalf("passenger 3 should NOT be available on legs 1,2")
	}
	_, err = Allocate(occupancy, mask3)
	if err != ErrSegmentCollision {
		t.Fatalf("expected ErrSegmentCollision, got: %v", err)
	}

	// Passenger 1 cancels: release legs 0,1
	occupancy = Release(occupancy, mask1)
	if occupancy != 0b1100 {
		t.Fatalf("after releasing passenger 1, expected 0b1100, got: %b", occupancy)
	}
}

func BenchmarkSegmentMask(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, _ = SegmentMask(2, 8)
	}
}

func BenchmarkAllocationCheck(b *testing.B) {
	occupancy := uint64(0b1010101010101010)
	requestMask := uint64(0b0101010101010101)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = IsAvailable(occupancy, requestMask)
	}
}
