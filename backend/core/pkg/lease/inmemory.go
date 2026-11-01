package lease

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

type InMemoryLeaseManager struct {
	mu           sync.Mutex
	inventory    map[string]int            // key -> available count
	activeLeases map[string]*LeaseMetadata // leaseToken -> metadata
	userLocks    map[string]string         // userID -> leaseToken
	// sorted expiry tracking: list of {expiresAt, leaseToken}
	expiryQueue []expiryItem
}

type expiryItem struct {
	expiresAt  int64
	leaseToken string
}

func NewInMemoryLeaseManager() *InMemoryLeaseManager {
	return &InMemoryLeaseManager{
		inventory:    make(map[string]int),
		activeLeases: make(map[string]*LeaseMetadata),
		userLocks:    make(map[string]string),
		expiryQueue:  make([]expiryItem, 0),
	}
}

// SetInitialInventory seeds available seat count for a specific class and quota.
func (m *InMemoryLeaseManager) SetInitialInventory(trainNumber, runDate, classCode, quotaCode string, count int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := BuildHashtagKey(trainNumber, runDate, fmt.Sprintf("inv:%s:%s", classCode, quotaCode))
	m.inventory[key] = count
}

func (m *InMemoryLeaseManager) GetAvailableInventory(trainNumber, runDate, classCode, quotaCode string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := BuildHashtagKey(trainNumber, runDate, fmt.Sprintf("inv:%s:%s", classCode, quotaCode))
	return m.inventory[key]
}

func (m *InMemoryLeaseManager) AcquireLease(ctx context.Context, req AcquireLeaseRequest) (*LeaseResult, error) {
	if req.PassengerCount < 1 || req.PassengerCount > 6 {
		return &LeaseResult{
			Success:      false,
			ErrorCode:    "INVALID_COUNT",
			ErrorMessage: ErrInvalidCount.Error(),
		}, ErrInvalidCount
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// 1. Strict Per-User Lease Cap (Active_Leases(User) <= 1)
	if existingToken, exists := m.userLocks[req.UserID]; exists {
		// Verify if the existing lease has expired
		if existingLease, ok := m.activeLeases[existingToken]; ok {
			if existingLease.ExpiresAt > time.Now().Unix() {
				return &LeaseResult{
					Success:      false,
					ErrorCode:    "ACTIVE_LEASE_EXISTS",
					ErrorMessage: fmt.Sprintf("%s (active token: %s)", ErrActiveLeaseExists.Error(), existingToken),
				}, ErrActiveLeaseExists
			}
			// Clean up expired lease inline
			m.cleanupLeaseLocked(existingToken)
		} else {
			delete(m.userLocks, req.UserID)
		}
	}

	// 2. Check inventory
	invKey := BuildHashtagKey(req.TrainNumber, req.RunDate, fmt.Sprintf("inv:%s:%s", req.ClassCode, req.QuotaCode))
	currentAvailable, exists := m.inventory[invKey]
	if !exists {
		return &LeaseResult{
			Success:      false,
			ErrorCode:    "INVENTORY_NOT_FOUND",
			ErrorMessage: "train run inventory key not found",
		}, fmt.Errorf("inventory key not found: %s", invKey)
	}

	if currentAvailable < req.PassengerCount {
		return &LeaseResult{
			Success:            false,
			RemainingInventory: currentAvailable,
			ErrorCode:          "INSUFFICIENT_INVENTORY",
			ErrorMessage:       ErrInsufficientInventory.Error(),
		}, ErrInsufficientInventory
	}

	// 3. Atomically decrement
	m.inventory[invKey] = currentAvailable - req.PassengerCount

	now := time.Now().Unix()
	ttl := req.TTLSeconds
	if ttl <= 0 {
		ttl = 300 // default 5 minutes
	}
	expiresAt := now + int64(ttl)
	leaseToken := uuid.New().String()

	metadata := &LeaseMetadata{
		LeaseToken:    leaseToken,
		TrainNumber:   req.TrainNumber,
		RunDate:       req.RunDate,
		ClassCode:     req.ClassCode,
		QuotaCode:     req.QuotaCode,
		OriginStopIdx: req.OriginStopIdx,
		DestStopIdx:   req.DestStopIdx,
		SegmentMask:   req.SegmentMask,
		Count:         req.PassengerCount,
		UserID:        req.UserID,
		InvKey:        invKey,
		CreatedAt:     now,
		ExpiresAt:     expiresAt,
	}

	m.activeLeases[leaseToken] = metadata
	m.userLocks[req.UserID] = leaseToken
	m.insertExpiryItemLocked(expiresAt, leaseToken)

	return &LeaseResult{
		Success:            true,
		LeaseToken:         leaseToken,
		ExpiresAt:          expiresAt,
		RemainingInventory: currentAvailable - req.PassengerCount,
	}, nil
}

func (m *InMemoryLeaseManager) ReleaseLease(ctx context.Context, trainNumber, runDate, leaseToken, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	lease, exists := m.activeLeases[leaseToken]
	if !exists {
		return ErrLeaseNotFound
	}

	if userID != "" && lease.UserID != userID {
		return ErrUserMismatch
	}

	// 1. Restore inventory
	m.inventory[lease.InvKey] += lease.Count

	// 2. Remove lease
	delete(m.activeLeases, leaseToken)
	delete(m.userLocks, lease.UserID)

	// 3. Remove from expiry queue
	m.removeExpiryItemLocked(leaseToken)

	return nil
}

func (m *InMemoryLeaseManager) GetLease(ctx context.Context, trainNumber, runDate, leaseToken string) (*LeaseMetadata, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	lease, exists := m.activeLeases[leaseToken]
	if !exists {
		return nil, ErrLeaseNotFound
	}
	if lease.ExpiresAt <= time.Now().Unix() {
		return nil, ErrLeaseExpired
	}

	// Return a copy
	cpy := *lease
	return &cpy, nil
}

// CommitLease finalizes the booking upon successful payment: converts held lease to committed,
// releasing the lock without returning inventory back to available pool.
func (m *InMemoryLeaseManager) CommitLease(ctx context.Context, trainNumber, runDate, leaseToken string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	lease, exists := m.activeLeases[leaseToken]
	if !exists {
		return ErrLeaseNotFound
	}
	if lease.ExpiresAt <= time.Now().Unix() {
		return ErrLeaseExpired
	}

	// Remove lease from active tracking and unlock user (inventory remains consumed)
	delete(m.activeLeases, leaseToken)
	delete(m.userLocks, lease.UserID)
	m.removeExpiryItemLocked(leaseToken)

	return nil
}

func (m *InMemoryLeaseManager) SweepExpiredLeases(ctx context.Context, trainNumber, runDate string, nowUnix int64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sweptCount := 0
	i := 0
	for i < len(m.expiryQueue) {
		item := m.expiryQueue[i]
		if item.expiresAt <= nowUnix {
			m.cleanupLeaseLocked(item.leaseToken)
			sweptCount++
			i++
		} else {
			break
		}
	}

	if i > 0 {
		m.expiryQueue = m.expiryQueue[i:]
	}

	return sweptCount, nil
}

func (m *InMemoryLeaseManager) cleanupLeaseLocked(leaseToken string) {
	lease, exists := m.activeLeases[leaseToken]
	if exists {
		// Restore inventory
		m.inventory[lease.InvKey] += lease.Count
		delete(m.activeLeases, leaseToken)
		delete(m.userLocks, lease.UserID)
	}
}

func (m *InMemoryLeaseManager) insertExpiryItemLocked(expiresAt int64, leaseToken string) {
	item := expiryItem{expiresAt: expiresAt, leaseToken: leaseToken}
	idx := len(m.expiryQueue)
	for i, existing := range m.expiryQueue {
		if existing.expiresAt > expiresAt {
			idx = i
			break
		}
	}
	if idx == len(m.expiryQueue) {
		m.expiryQueue = append(m.expiryQueue, item)
	} else {
		m.expiryQueue = append(m.expiryQueue[:idx], append([]expiryItem{item}, m.expiryQueue[idx:]...)...)
	}
}

func (m *InMemoryLeaseManager) removeExpiryItemLocked(leaseToken string) {
	for i, item := range m.expiryQueue {
		if item.leaseToken == leaseToken {
			m.expiryQueue = append(m.expiryQueue[:i], m.expiryQueue[i+1:]...)
			return
		}
	}
}
