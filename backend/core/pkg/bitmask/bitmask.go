package bitmask

import (
	"errors"
	"fmt"
)

var (
	ErrInvalidStopRange = errors.New("invalid stop index range: origin must be less than destination")
	ErrMaxStopsExceeded = errors.New("maximum 64 station stops supported by 64-bit segment mask")
	ErrSegmentCollision = errors.New("berth segment collision: segment already occupied")
)

// SegmentMask computes the 64-bit bitmask representing the required station hops
// for a journey from originStopIdx to destStopIdx.
// Hops are represented as: leg k = station[k] -> station[k+1], bit value = 1 << k.
func SegmentMask(originStopIdx, destStopIdx int) (uint64, error) {
	if originStopIdx < 0 || destStopIdx <= originStopIdx {
		return 0, fmt.Errorf("%w: origin=%d, destination=%d", ErrInvalidStopRange, originStopIdx, destStopIdx)
	}
	if destStopIdx > 64 {
		return 0, fmt.Errorf("%w: destStopIdx=%d (max 64)", ErrMaxStopsExceeded, destStopIdx)
	}

	hops := destStopIdx - originStopIdx
	if hops >= 64 {
		return ^uint64(0), nil
	}

	// ((1 << hops) - 1) << originStopIdx
	mask := ((uint64(1) << hops) - 1) << originStopIdx
	return mask, nil
}

// IsAvailable checks if the berth with occupancyMask has all requested segment hops free.
func IsAvailable(occupancyMask, requestMask uint64) bool {
	return (occupancyMask & requestMask) == 0
}

// Allocate marks the requested segment hops as occupied on the berth.
// Returns an error if there is any segment collision.
func Allocate(occupancyMask, requestMask uint64) (uint64, error) {
	if !IsAvailable(occupancyMask, requestMask) {
		return occupancyMask, ErrSegmentCollision
	}
	return occupancyMask | requestMask, nil
}

// Release frees the specified segment hops from the berth occupancy mask.
func Release(occupancyMask, releaseMask uint64) uint64 {
	return occupancyMask &^ releaseMask
}

// Overlaps checks whether two segment masks have any shared hops.
func Overlaps(maskA, maskB uint64) bool {
	return (maskA & maskB) != 0
}
