package lease

import (
	"context"
	"log"
	"sync"
	"time"
)

type SweeperDaemon struct {
	manager  SegmentLeaseManager
	interval time.Duration
	stopCh   chan struct{}
	wg       sync.WaitGroup

	// active train runs to monitor: map of "train:date"
	mu              sync.RWMutex
	monitoredRuns   map[string]runKey
	totalSweptCount int64
}

type runKey struct {
	TrainNumber string
	RunDate     string
}

func NewSweeperDaemon(manager SegmentLeaseManager, interval time.Duration) *SweeperDaemon {
	if interval <= 0 {
		interval = 1 * time.Second
	}
	return &SweeperDaemon{
		manager:       manager,
		interval:      interval,
		stopCh:        make(chan struct{}),
		monitoredRuns: make(map[string]runKey),
	}
}

func (s *SweeperDaemon) RegisterRun(trainNumber, runDate string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := trainNumber + ":" + runDate
	s.monitoredRuns[key] = runKey{TrainNumber: trainNumber, RunDate: runDate}
}

func (s *SweeperDaemon) UnregisterRun(trainNumber, runDate string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.monitoredRuns, trainNumber+":"+runDate)
}

func (s *SweeperDaemon) Start() {
	s.wg.Add(1)
	go s.runLoop()
}

func (s *SweeperDaemon) Stop() {
	close(s.stopCh)
	s.wg.Wait()
}

func (s *SweeperDaemon) runLoop() {
	defer s.wg.Done()
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.executeSweepCycle()
		}
	}
}

func (s *SweeperDaemon) executeSweepCycle() {
	s.mu.RLock()
	runs := make([]runKey, 0, len(s.monitoredRuns))
	for _, r := range s.monitoredRuns {
		runs = append(runs, r)
	}
	s.mu.RUnlock()

	now := time.Now().Unix()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	for _, r := range runs {
		swept, err := s.manager.SweepExpiredLeases(ctx, r.TrainNumber, r.RunDate, now)
		if err != nil {
			log.Printf("[SWEEPER] Error sweeping run %s:%s: %v", r.TrainNumber, r.RunDate, err)
			continue
		}
		if swept > 0 {
			log.Printf("[SWEEPER] Reclaimed %d expired seat leases for train %s on %s", swept, r.TrainNumber, r.RunDate)
		}
	}
}
