package lease

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type RedisLeaseManager struct {
	client         redis.UniversalClient
	leaseScriptSHA string
	releaseScriptSHA string
}

func NewRedisLeaseManager(client redis.UniversalClient) (*RedisLeaseManager, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	leaseSHA, err := client.ScriptLoad(ctx, leaseSegmentLua).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to load lease_segment.lua: %w", err)
	}

	releaseSHA, err := client.ScriptLoad(ctx, releaseLeaseLua).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to load release_lease.lua: %w", err)
	}

	return &RedisLeaseManager{
		client:           client,
		leaseScriptSHA:   leaseSHA,
		releaseScriptSHA: releaseSHA,
	}, nil
}

func (r *RedisLeaseManager) SeedInventory(ctx context.Context, trainNumber, runDate, classCode, quotaCode string, count int) error {
	invKey := BuildHashtagKey(trainNumber, runDate, fmt.Sprintf("inv:%s:%s", classCode, quotaCode))
	return r.client.Set(ctx, invKey, count, 24*time.Hour).Err()
}

func (r *RedisLeaseManager) AcquireLease(ctx context.Context, req AcquireLeaseRequest) (*LeaseResult, error) {
	if req.PassengerCount < 1 || req.PassengerCount > 6 {
		return &LeaseResult{
			Success:      false,
			ErrorCode:    "INVALID_COUNT",
			ErrorMessage: ErrInvalidCount.Error(),
		}, ErrInvalidCount
	}

	invKey := BuildHashtagKey(req.TrainNumber, req.RunDate, fmt.Sprintf("inv:%s:%s", req.ClassCode, req.QuotaCode))
	activeLeasesKey := BuildHashtagKey(req.TrainNumber, req.RunDate, "active_leases")
	expiryZsetKey := BuildHashtagKey(req.TrainNumber, req.RunDate, "lease_expiry_zset")
	userLockKey := BuildUserLockKey(req.UserID)

	leaseToken := uuid.New().String()
	ttl := req.TTLSeconds
	if ttl <= 0 {
		ttl = 300
	}

	keys := []string{invKey, activeLeasesKey, expiryZsetKey, userLockKey}
	args := []interface{}{
		req.PassengerCount,
		req.UserID,
		ttl,
		leaseToken,
		fmt.Sprintf("%d", req.SegmentMask),
	}

	res, err := r.client.EvalSha(ctx, r.leaseScriptSHA, keys, args...).Slice()
	if err != nil {
		return &LeaseResult{
			Success:      false,
			ErrorCode:    "REDIS_EXEC_ERROR",
			ErrorMessage: err.Error(),
		}, err
	}

	successCode := res[0].(int64)
	if successCode == 0 {
		reason := res[1].(string)
		if reason == "ERR_USER_ACTIVE_LEASE_EXISTS" {
			existingToken := res[2].(string)
			return &LeaseResult{
				Success:      false,
				ErrorCode:    "ACTIVE_LEASE_EXISTS",
				ErrorMessage: fmt.Sprintf("%s (active token: %s)", ErrActiveLeaseExists.Error(), existingToken),
			}, ErrActiveLeaseExists
		}
		if reason == "INSUFFICIENT_INVENTORY" {
			avail := int(res[2].(int64))
			return &LeaseResult{
				Success:            false,
				RemainingInventory: avail,
				ErrorCode:          "INSUFFICIENT_INVENTORY",
				ErrorMessage:       ErrInsufficientInventory.Error(),
			}, ErrInsufficientInventory
		}
		return &LeaseResult{
			Success:      false,
			ErrorCode:    reason,
			ErrorMessage: reason,
		}, errors.New(reason)
	}

	token := res[1].(string)
	remaining := int(res[2].(int64))
	expiresAt := res[3].(int64)

	return &LeaseResult{
		Success:            true,
		LeaseToken:         token,
		ExpiresAt:          expiresAt,
		RemainingInventory: remaining,
	}, nil
}

func (r *RedisLeaseManager) ReleaseLease(ctx context.Context, trainNumber, runDate, leaseToken, userID string) error {
	activeLeasesKey := BuildHashtagKey(trainNumber, runDate, "active_leases")
	expiryZsetKey := BuildHashtagKey(trainNumber, runDate, "lease_expiry_zset")
	userLockKey := BuildUserLockKey(userID)

	keys := []string{activeLeasesKey, expiryZsetKey, userLockKey}
	args := []interface{}{leaseToken, userID}

	res, err := r.client.EvalSha(ctx, r.releaseScriptSHA, keys, args...).Slice()
	if err != nil {
		return err
	}

	status := res[0].(int64)
	if status == 0 {
		return errors.New(res[1].(string))
	}

	return nil
}

func (r *RedisLeaseManager) GetLease(ctx context.Context, trainNumber, runDate, leaseToken string) (*LeaseMetadata, error) {
	activeLeasesKey := BuildHashtagKey(trainNumber, runDate, "active_leases")
	val, err := r.client.HGet(ctx, activeLeasesKey, leaseToken).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, ErrLeaseNotFound
		}
		return nil, err
	}

	var meta LeaseMetadata
	if err := json.Unmarshal([]byte(val), &meta); err != nil {
		return nil, err
	}

	if meta.ExpiresAt <= time.Now().Unix() {
		return nil, ErrLeaseExpired
	}

	return &meta, nil
}

func (r *RedisLeaseManager) CommitLease(ctx context.Context, trainNumber, runDate, leaseToken string) error {
	activeLeasesKey := BuildHashtagKey(trainNumber, runDate, "active_leases")
	expiryZsetKey := BuildHashtagKey(trainNumber, runDate, "lease_expiry_zset")

	// Read lease to find user ID
	val, err := r.client.HGet(ctx, activeLeasesKey, leaseToken).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return ErrLeaseNotFound
		}
		return err
	}

	var meta LeaseMetadata
	if err := json.Unmarshal([]byte(val), &meta); err != nil {
		return err
	}

	if meta.ExpiresAt <= time.Now().Unix() {
		return ErrLeaseExpired
	}

	pipe := r.client.TxPipeline()
	pipe.HDel(ctx, activeLeasesKey, leaseToken)
	pipe.ZRem(ctx, expiryZsetKey, leaseToken)
	pipe.Del(ctx, BuildUserLockKey(meta.UserID))
	_, err = pipe.Exec(ctx)
	return err
}

func (r *RedisLeaseManager) SweepExpiredLeases(ctx context.Context, trainNumber, runDate string, nowUnix int64) (int, error) {
	expiryZsetKey := BuildHashtagKey(trainNumber, runDate, "lease_expiry_zset")

	expiredTokens, err := r.client.ZRangeByScore(ctx, expiryZsetKey, &redis.ZRangeBy{
		Min: "-inf",
		Max: fmt.Sprintf("%d", nowUnix),
	}).Result()

	if err != nil || len(expiredTokens) == 0 {
		return 0, err
	}

	sweptCount := 0
	for _, token := range expiredTokens {
		// Release each expired token
		_ = r.ReleaseLease(ctx, trainNumber, runDate, token, "")
		sweptCount++
	}

	return sweptCount, nil
}
