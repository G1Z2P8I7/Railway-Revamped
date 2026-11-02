package payment

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

type TransitLedgerManager struct {
	mu       sync.RWMutex
	accounts map[string]*TransitLedgerAccount // userID -> account
}

func NewTransitLedgerManager() *TransitLedgerManager {
	return &TransitLedgerManager{
		accounts: make(map[string]*TransitLedgerAccount),
	}
}

// GetAccount returns or initializes a user's closed-loop transit ledger account.
func (m *TransitLedgerManager) GetAccount(userID string) *TransitLedgerAccount {
	m.mu.Lock()
	defer m.mu.Unlock()

	acc, exists := m.accounts[userID]
	if !exists {
		acc = &TransitLedgerAccount{
			UserID:      userID,
			BalanceINR:  0.00,
			LastUpdated: time.Now(),
			Entries:     make([]TransitLedgerEntry, 0),
		}
		m.accounts[userID] = acc
	}
	return acc
}

// CreditInstantRefund issues instant (<5 seconds) credit directly into the closed-loop ledger.
func (m *TransitLedgerManager) CreditInstantRefund(userID, txnID string, amount float64, note string) (*TransitLedgerEntry, error) {
	if amount <= 0 {
		return nil, fmt.Errorf("invalid credit amount: %f", amount)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	acc, exists := m.accounts[userID]
	if !exists {
		acc = &TransitLedgerAccount{
			UserID:      userID,
			BalanceINR:  0.00,
			LastUpdated: time.Now(),
			Entries:     make([]TransitLedgerEntry, 0),
		}
		m.accounts[userID] = acc
	}

	now := time.Now()
	acc.BalanceINR += amount
	acc.LastUpdated = now

	entryBytes := make([]byte, 12)
	_, _ = rand.Read(entryBytes)
	entryID := "tle_" + hex.EncodeToString(entryBytes)

	entry := TransitLedgerEntry{
		EntryID:   entryID,
		UserID:    userID,
		TxnID:     txnID,
		AmountINR: amount,
		EntryType: "INSTANT_REFUND",
		Note:      note,
		Timestamp: now,
	}

	acc.Entries = append(acc.Entries, entry)
	return &entry, nil
}

// DebitForBooking uses transit ledger funds for ticket checkout.
func (m *TransitLedgerManager) DebitForBooking(userID, txnID string, amount float64) (*TransitLedgerEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	acc, exists := m.accounts[userID]
	if !exists || acc.BalanceINR < amount {
		return nil, ErrInsufficientLedgerBal
	}

	now := time.Now()
	acc.BalanceINR -= amount
	acc.LastUpdated = now

	entryBytes := make([]byte, 12)
	_, _ = rand.Read(entryBytes)
	entryID := "tle_" + hex.EncodeToString(entryBytes)

	entry := TransitLedgerEntry{
		EntryID:   entryID,
		UserID:    userID,
		TxnID:     txnID,
		AmountINR: -amount,
		EntryType: "BOOKING_DEBIT",
		Note:      fmt.Sprintf("Payment for transaction %s", txnID),
		Timestamp: now,
	}

	acc.Entries = append(acc.Entries, entry)
	return &entry, nil
}

func (m *TransitLedgerManager) GetBalance(userID string) float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if acc, exists := m.accounts[userID]; exists {
		return acc.BalanceINR
	}
	return 0.00
}
