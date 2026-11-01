package lease

import (
	"context"
	"errors"
	"fmt"
)

var (
	ErrActiveLeaseExists   = errors.New("user already has an active seat lease in progress")
	ErrInsufficientInventory = errors.New("insufficient inventory available for requested segment")
	ErrLeaseNotFound       = errors.New("lease token not found or already expired")
	ErrLeaseExpired        = errors.New("lease has expired")
	ErrUserMismatch        = errors.New("lease does not belong to the requesting user")
	ErrInvalidCount        = errors.New("passenger count must be between 1 and 6")
)

type LeaseMetadata struct {
	LeaseToken     string `json:"lease_token"`
	TrainNumber    string `json:"train_number"`
	RunDate        string `json:"run_date"`
	ClassCode      string `json:"class_code"`
	QuotaCode      string `json:"quota_code"`
	OriginStopIdx  int    `json:"origin_stop_idx"`
	DestStopIdx    int    `json:"dest_stop_idx"`
	SegmentMask    uint64 `json:"seg_mask"`
	Count          int    `json:"count"`
	UserID         string `json:"user_id"`
	InvKey         string `json:"inv_key"`
	CreatedAt      int64  `json:"created_at"`
	ExpiresAt      int64  `json:"expires_at"`
}

type AcquireLeaseRequest struct {
	UserID         string
	TrainNumber    string
	RunDate        string
	ClassCode      string
	QuotaCode      string
	OriginStopIdx  int
	DestStopIdx    int
	SegmentMask    uint64
	PassengerCount int
	TTLSeconds     int
}

type LeaseResult struct {
	Success            bool
	LeaseToken         string
	ExpiresAt          int64
	RemainingInventory int
	ErrorCode          string
	ErrorMessage       string
}

type SegmentLeaseManager interface {
	AcquireLease(ctx context.Context, req AcquireLeaseRequest) (*LeaseResult, error)
	ReleaseLease(ctx context.Context, trainNumber, runDate, leaseToken, userID string) error
	GetLease(ctx context.Context, trainNumber, runDate, leaseToken string) (*LeaseMetadata, error)
	CommitLease(ctx context.Context, trainNumber, runDate, leaseToken string) error
	SweepExpiredLeases(ctx context.Context, trainNumber, runDate string, nowUnix int64) (int, error)
}

// BuildHashtagKey creates a Redis cluster hash tag ensuring all keys for a specific
// train run route map to the same hash slot.
func BuildHashtagKey(trainNumber, runDate, suffix string) string {
	return fmt.Sprintf("{%s:%s}:%s", trainNumber, runDate, suffix)
}

func BuildUserLockKey(userID string) string {
	return fmt.Sprintf("user:%s:active_lease", userID)
}
