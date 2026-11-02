package charting

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/railway-revamped/core/pkg/solver"
)

type ChartPreparationDaemon struct {
	mu           sync.RWMutex
	monitored    map[string]*ScheduledTrainRun // trainNumber:runDate -> run
	sweeper      *QuotaSweeper
	familySolver *solver.SpatialFamilySolver
	stopCh       chan struct{}
	interval     time.Duration
	timeProvider func() time.Time
}

func NewChartPreparationDaemon(interval time.Duration, timeProvider func() time.Time) *ChartPreparationDaemon {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	if timeProvider == nil {
		timeProvider = time.Now
	}
	return &ChartPreparationDaemon{
		monitored:    make(map[string]*ScheduledTrainRun),
		sweeper:      NewQuotaSweeper(),
		familySolver: solver.NewSpatialFamilySolver(),
		stopCh:       make(chan struct{}),
		interval:     interval,
		timeProvider: timeProvider,
	}
}

func (d *ChartPreparationDaemon) RegisterTrainRun(run *ScheduledTrainRun) {
	d.mu.Lock()
	defer d.mu.Unlock()
	key := run.TrainNumber + ":" + run.RunDate
	d.monitored[key] = run
}

func (d *ChartPreparationDaemon) Start(ctx context.Context) {
	ticker := time.NewTicker(d.interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-d.stopCh:
				return
			case <-ticker.C:
				d.CheckAndPrepareCharts()
			}
		}
	}()
}

func (d *ChartPreparationDaemon) Stop() {
	close(d.stopCh)
}

// CheckAndPrepareCharts sweeps runs within 8 hours of origin departure.
func (d *ChartPreparationDaemon) CheckAndPrepareCharts() int {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := d.timeProvider()
	chartsPrepared := 0

	for _, run := range d.monitored {
		if run.ChartingState != ChartPending {
			continue
		}

		timeUntilDeparture := run.ScheduledDeparture.Sub(now)
		// 8-hour window (<= 8 hours before departure)
		if timeUntilDeparture <= 8*time.Hour {
			d.executeChartPreparation(run, now)
			chartsPrepared++
		}
	}

	return chartsPrepared
}

func (d *ChartPreparationDaemon) executeChartPreparation(run *ScheduledTrainRun, now time.Time) {
	log.Printf("[CHARTING] Executing 8-Hour Chart Preparation for Train %s (%s) departing at %s",
		run.TrainNumber, run.TrainName, run.ScheduledDeparture.Format("2006-01-02 15:04"))

	// 1. Sweep unutilized VIP / Defence / Foreign Tourist quotas
	swept := d.sweeper.SweepUnutilizedQuotas(run)

	// 2. Cascade freed berths into RAC and Waitlist
	promoted := d.sweeper.CascadeFreedBerths(run)

	// 3. Physical Coach & Berth Assignment via Spatial Solver
	d.assignPhysicalBerths(run)

	// 4. Pair remaining RAC passengers (2 per Side Lower berth)
	d.pairRACSideLowerBerths(run)

	// 5. Update status
	run.ChartingState = ChartPrepared
	run.ChartingTime = now

	// 6. If empty seats remain, open Current Booking
	if run.QuotaPools[QuotaGeneral] > 0 {
		run.ChartingState = ChartCurrentBooking
		run.CurrentBookingOpen = true
		log.Printf("[CHARTING] %d berths opened for Current Booking on train %s",
			run.QuotaPools[QuotaGeneral], run.TrainNumber)
	}

	log.Printf("[CHARTING] Chart Prepared successfully: %d swept, %d promoted. Final state: %s",
		swept, promoted, run.ChartingState)
}

func (d *ChartPreparationDaemon) assignPhysicalBerths(run *ScheduledTrainRun) {
	if len(run.Coaches) == 0 {
		return
	}

	// Group confirmed passengers by PNR for spatial family cohesion
	pnrGroups := make(map[string][]solver.PassengerRequest)
	passengerMap := make(map[string]*TicketPassenger)

	for _, p := range run.ConfirmedList {
		passengerMap[p.PassengerID] = p
		req := solver.PassengerRequest{
			PassengerID:     p.PassengerID,
			FullName:        p.FullName,
			Age:             p.Age,
			Gender:          p.Gender,
			IsSeniorCitizen: p.IsSeniorCitizen,
			Preference:      p.Preference,
		}
		pnrGroups[p.PNRNumber] = append(pnrGroups[p.PNRNumber], req)
	}

	// Run spatial solver for each family/group PNR
	for _, group := range pnrGroups {
		allocations, err := d.familySolver.AllocateFamily(run.Coaches, group)
		if err != nil {
			log.Printf("[CHARTING] Warning: Family allocation solver fallback: %v", err)
			continue
		}

		for _, alloc := range allocations {
			if target, ok := passengerMap[alloc.PassengerID]; ok {
				target.CoachID = alloc.CoachID
				target.BerthNumber = alloc.BerthNumber
				target.BerthType = alloc.BerthType
			}
		}
	}
}

// pairRACSideLowerBerths assigns 2 RAC passengers to share a single Side Lower (SL) berth.
func (d *ChartPreparationDaemon) pairRACSideLowerBerths(run *ScheduledTrainRun) {
	if len(run.RACList) == 0 || len(run.Coaches) == 0 {
		return
	}

	// Find available Side Lower berths across coaches
	var sideLowerBerths []*solver.PhysicalBerth
	for _, c := range run.Coaches {
		for i := range c.Berths {
			b := &c.Berths[i]
			if b.BerthType == solver.BerthSideLower && !b.Occupied {
				sideLowerBerths = append(sideLowerBerths, b)
			}
		}
	}

	for i, racPassenger := range run.RACList {
		berthIdx := i / 2 // 2 passengers share each berth
		if berthIdx < len(sideLowerBerths) {
			b := sideLowerBerths[berthIdx]
			racPassenger.CoachID = b.CoachID
			racPassenger.BerthNumber = b.BerthNumber
			racPassenger.BerthType = solver.BerthSideLower
			// Mark occupied only when second passenger is seated
			if i%2 == 1 {
				b.Occupied = true
			}
		}
	}
}

func (d *ChartPreparationDaemon) GetRunState(trainNumber, runDate string) (*ScheduledTrainRun, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	key := trainNumber + ":" + runDate
	run, exists := d.monitored[key]
	if !exists {
		return nil, fmt.Errorf("run not found: %s", key)
	}
	return run, nil
}
