package ingress

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"
)

var (
	ErrLobbyCapacityExceeded = errors.New("virtual waiting lobby queue capacity reached")
	ErrInvalidLobbyTicket    = errors.New("invalid or expired lobby admission ticket")
)

type ClientType string

const (
	ClientCitizen    ClientType = "CITIZEN"
	ClientAggregator ClientType = "COMMERCIAL_AGGREGATOR"
	ClientAgent      ClientType = "TRAVEL_AGENT"
)

type LobbyEntry struct {
	TicketID     string
	UserID       string
	ClientType   ClientType
	TrainNumber  string
	RunDate      string
	ClassCode    string
	ArrivedAt    time.Time
	BucketIndex  int64
	DispatchTime time.Time
}

type AdmissionTicket struct {
	TicketID      string     `json:"ticket_id"`
	UserID        string     `json:"user_id"`
	BucketIndex   int64      `json:"bucket_index"`
	QueuePosition int        `json:"queue_position"`
	EstimatedWait int64      `json:"estimated_wait_ms"`
	ReadyAtUnixMs int64      `json:"ready_at_unix_ms"`
	AdmissionToken string    `json:"admission_token"`
}

type VirtualWaitingLobby struct {
	mu           sync.Mutex
	bucketDuration time.Duration // default 500ms
	maxCapacity    int
	queue          []*LobbyEntry
	admittedMap    map[string]time.Time // token -> admitted at
}

func NewVirtualWaitingLobby(bucketDuration time.Duration, maxCapacity int) *VirtualWaitingLobby {
	if bucketDuration <= 0 {
		bucketDuration = 500 * time.Millisecond
	}
	if maxCapacity <= 0 {
		maxCapacity = 1000000 // 1 Million queue buffer
	}
	return &VirtualWaitingLobby{
		bucketDuration: bucketDuration,
		maxCapacity:    maxCapacity,
		queue:          make([]*LobbyEntry, 0),
		admittedMap:    make(map[string]time.Time),
	}
}

// EnqueueRequest places an incoming user into the randomized 500ms jitter bucket.
func (l *VirtualWaitingLobby) EnqueueRequest(ctx context.Context, userID string, clientType ClientType, trainNumber, runDate, classCode string) (*AdmissionTicket, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.queue) >= l.maxCapacity {
		return nil, ErrLobbyCapacityExceeded
	}

	now := time.Now()
	bucketDurationMs := l.bucketDuration.Milliseconds()
	currentBucket := now.UnixMilli() / bucketDurationMs

	// Compute randomized jitter within the 500ms bucket window [0, bucketDurationMs)
	randomOffset, _ := rand.Int(rand.Reader, big.NewInt(bucketDurationMs))
	dispatchTime := now.Add(time.Duration(randomOffset.Int64()) * time.Millisecond)

	ticketBytes := make([]byte, 16)
	_, _ = rand.Read(ticketBytes)
	ticketID := "tkt_" + hex.EncodeToString(ticketBytes)

	entry := &LobbyEntry{
		TicketID:     ticketID,
		UserID:       userID,
		ClientType:   clientType,
		TrainNumber:  trainNumber,
		RunDate:      runDate,
		ClassCode:    classCode,
		ArrivedAt:    now,
		BucketIndex:  currentBucket,
		DispatchTime: dispatchTime,
	}

	l.queue = append(l.queue, entry)
	position := len(l.queue)

	// Issue admission token
	tokenBytes := make([]byte, 24)
	_, _ = rand.Read(tokenBytes)
	admissionToken := "adm_" + hex.EncodeToString(tokenBytes)
	l.admittedMap[admissionToken] = dispatchTime

	return &AdmissionTicket{
		TicketID:       ticketID,
		UserID:         userID,
		BucketIndex:    currentBucket,
		QueuePosition:  position,
		EstimatedWait:  dispatchTime.Sub(now).Milliseconds(),
		ReadyAtUnixMs:  dispatchTime.UnixMilli(),
		AdmissionToken: admissionToken,
	}, nil
}

// ValidateAdmissionToken checks if the client has waited for their 500ms jitter turn.
func (l *VirtualWaitingLobby) ValidateAdmissionToken(token string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	readyTime, exists := l.admittedMap[token]
	if !exists {
		return ErrInvalidLobbyTicket
	}

	if time.Now().Before(readyTime) {
		remaining := readyTime.Sub(time.Now()).Milliseconds()
		return fmt.Errorf("lobby token not ready yet: %d ms remaining", remaining)
	}

	// Token is valid and ready, consume it
	delete(l.admittedMap, token)
	return nil
}
