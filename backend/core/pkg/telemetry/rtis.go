package telemetry

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"
)

// SignalAspect represents the trackside signaling state
type SignalAspect string

const (
	SignalGreen        SignalAspect = "GREEN"
	SignalDoubleYellow SignalAspect = "DOUBLE_YELLOW"
	SignalYellow       SignalAspect = "YELLOW"
	SignalRed          SignalAspect = "RED"
)

// TelemetryPoint represents a single RTIS locomotive transponder reading
type TelemetryPoint struct {
	TrainNumber         string       `json:"train_number"`
	LocoID              string       `json:"loco_id"`
	Timestamp           time.Time    `json:"timestamp"`
	Latitude            float64      `json:"latitude"`
	Longitude           float64      `json:"longitude"`
	SpeedKmph           float64      `json:"speed_kmph"`
	HeadingDegrees      float64      `json:"heading_degrees"`
	CurrentStationCode  string       `json:"current_station_code"`
	NextStationCode     string       `json:"next_station_code"`
	CurrentBlockSection string       `json:"current_block_section"`
	DelayMinutes        int          `json:"delay_minutes"`
	SignalAspect        SignalAspect `json:"signal_aspect"`
	SatelliteLock       bool         `json:"satellite_lock"`
	IsDeadReckoning     bool         `json:"is_dead_reckoning"`
}

// TrainScheduleStop represents a station stop in a train's static master timetable
type TrainScheduleStop struct {
	StationCode     string
	ScheduledArrival time.Time
	ScheduledDeparture time.Time
	DistanceKM      float64
}

// TransitAlert represents an automated notification dispatched when transit metrics deviate
type TransitAlert struct {
	AlertID      string       `json:"alert_id"`
	TrainNumber  string       `json:"train_number"`
	Timestamp    time.Time    `json:"timestamp"`
	AlertType    string       `json:"alert_type"` // e.g. "MAJOR_DELAY", "DIVERSION", "SPEED_RESTRICTION"
	DelayMinutes int          `json:"delay_minutes"`
	Description  string       `json:"description"`
}

// IngestionStats tracks real-time telemetry metrics
type IngestionStats struct {
	TotalIngested uint64
	BatchesWritten uint64
	DeadReckoned   uint64
	AlertsEmitted  uint64
}

// AlertHandler callback function signature
type AlertHandler func(alert TransitAlert)

// RTISIngestionEngine manages high-throughput locomotive GPS updates (~467 writes/s nationwide)
type RTISIngestionEngine struct {
	mu           sync.RWMutex
	pointBuffer  chan TelemetryPoint
	trainLatest  map[string]TelemetryPoint
	trainHistory map[string][]TelemetryPoint // Ring-buffered history per train
	timetables   map[string][]TrainScheduleStop
	alertHandlers []AlertHandler
	stats        IngestionStats
	stopChan     chan struct{}
	batchSize    int
	flushInterval time.Duration
	maxHistory   int
}

// NewRTISIngestionEngine initializes the high-throughput transponder ingestion engine
func NewRTISIngestionEngine(bufferCap int, batchSize int, flushInterval time.Duration) *RTISIngestionEngine {
	if bufferCap <= 0 {
		bufferCap = 50000
	}
	if batchSize <= 0 {
		batchSize = 500
	}
	if flushInterval <= 0 {
		flushInterval = 100 * time.Millisecond
	}

	return &RTISIngestionEngine{
		pointBuffer:  make(chan TelemetryPoint, bufferCap),
		trainLatest:  make(map[string]TelemetryPoint),
		trainHistory: make(map[string][]TelemetryPoint),
		timetables:   make(map[string][]TrainScheduleStop),
		alertHandlers: make([]AlertHandler, 0),
		stopChan:     make(chan struct{}),
		batchSize:    batchSize,
		flushInterval: flushInterval,
		maxHistory:   500, // Retain last 500 telemetry points per train in-memory
	}
}

// RegisterTimetable loads static scheduled times for delay calculation
func (e *RTISIngestionEngine) RegisterTimetable(trainNumber string, stops []TrainScheduleStop) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.timetables[trainNumber] = stops
}

// RegisterAlertHandler subscribes a listener to transit deviation alerts
func (e *RTISIngestionEngine) RegisterAlertHandler(handler AlertHandler) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.alertHandlers = append(e.alertHandlers, handler)
}

// SubmitTelemetry pushes a raw transponder reading into the non-blocking ingestion stream
func (e *RTISIngestionEngine) SubmitTelemetry(point TelemetryPoint) error {
	select {
	case e.pointBuffer <- point:
		return nil
	default:
		return fmt.Errorf("rtis ingestion buffer full (dropping frame)")
	}
}

// Start launches the background batch aggregator worker
func (e *RTISIngestionEngine) Start(ctx context.Context) {
	go e.batchProcessor(ctx)
}

// Stop cleanly terminates the batch processor
func (e *RTISIngestionEngine) Stop() {
	close(e.stopChan)
}

// batchProcessor aggregates incoming telemetry into partitioned batch writes
func (e *RTISIngestionEngine) batchProcessor(ctx context.Context) {
	ticker := time.NewTicker(e.flushInterval)
	defer ticker.Stop()

	batch := make([]TelemetryPoint, 0, e.batchSize)

	flush := func() {
		if len(batch) == 0 {
			return
		}
		e.processBatch(batch)
		batch = make([]TelemetryPoint, 0, e.batchSize)
	}

	for {
		select {
		case <-ctx.Done():
			flush()
			return
		case <-e.stopChan:
			flush()
			return
		case pt := <-e.pointBuffer:
			batch = append(batch, pt)
			if len(batch) >= e.batchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// processBatch handles dead-reckoning extrapolation, delay calculation, and storage commit
func (e *RTISIngestionEngine) processBatch(batch []TelemetryPoint) {
	e.mu.Lock()
	defer e.mu.Unlock()

	var alertsToEmit []TransitAlert

	for i := range batch {
		pt := batch[i]

		// 1. Dead-Reckoning calculation if satellite lock lost (e.g. tunnels/mountain valleys)
		if !pt.SatelliteLock {
			last, exists := e.trainLatest[pt.TrainNumber]
			if exists {
				pt = e.computeDeadReckoning(last, pt)
				e.stats.DeadReckoned++
			}
		}

		// 2. Automated delay calculation against static schedule
		calculatedDelay := e.evaluateDelay(pt)
		if calculatedDelay > 0 {
			pt.DelayMinutes = calculatedDelay
		}

		// 3. Automated Alert Triggering (Delays >= 30 minutes, or major delay increase)
		last, hasLast := e.trainLatest[pt.TrainNumber]
		if pt.DelayMinutes >= 30 {
			if !hasLast || last.DelayMinutes < 30 || (pt.DelayMinutes-last.DelayMinutes >= 15) {
				alert := TransitAlert{
					AlertID:      fmt.Sprintf("ALT-%s-%d", pt.TrainNumber, time.Now().UnixNano()),
					TrainNumber:  pt.TrainNumber,
					Timestamp:    pt.Timestamp,
					AlertType:    "MAJOR_DELAY",
					DelayMinutes: pt.DelayMinutes,
					Description:  fmt.Sprintf("Train %s is running late by %d minutes near station %s", pt.TrainNumber, pt.DelayMinutes, pt.CurrentStationCode),
				}
				alertsToEmit = append(alertsToEmit, alert)
				e.stats.AlertsEmitted++
			}
		}

		// 4. Update latest state and ring-buffer history (simulating TimescaleDB/ClickHouse partitions)
		e.trainLatest[pt.TrainNumber] = pt
		history := e.trainHistory[pt.TrainNumber]
		if len(history) >= e.maxHistory {
			history = history[1:]
		}
		e.trainHistory[pt.TrainNumber] = append(history, pt)
		e.stats.TotalIngested++
	}

	e.stats.BatchesWritten++

	// Dispatch alerts asynchronously to avoid blocking ingestion mutex
	if len(alertsToEmit) > 0 && len(e.alertHandlers) > 0 {
		for _, alert := range alertsToEmit {
			for _, handler := range e.alertHandlers {
				go handler(alert)
			}
		}
	}
}

// computeDeadReckoning extrapolates coordinates using last known speed, heading, and elapsed time
func (e *RTISIngestionEngine) computeDeadReckoning(last TelemetryPoint, current TelemetryPoint) TelemetryPoint {
	elapsedSeconds := current.Timestamp.Sub(last.Timestamp).Seconds()
	if elapsedSeconds <= 0 {
		elapsedSeconds = 1
	}

	// Average speed in m/s
	speedMs := ((last.SpeedKmph + current.SpeedKmph) / 2.0) * (1000.0 / 3600.0)
	distanceMeters := speedMs * elapsedSeconds

	// Earth radius in meters
	const earthRadius = 6371000.0
	headingRad := last.HeadingDegrees * (math.Pi / 180.0)

	latRad := last.Latitude * (math.Pi / 180.0)
	lonRad := last.Longitude * (math.Pi / 180.0)

	newLatRad := math.Asin(math.Sin(latRad)*math.Cos(distanceMeters/earthRadius) +
		math.Cos(latRad)*math.Sin(distanceMeters/earthRadius)*math.Cos(headingRad))

	newLonRad := lonRad + math.Atan2(math.Sin(headingRad)*math.Sin(distanceMeters/earthRadius)*math.Cos(latRad),
		math.Cos(distanceMeters/earthRadius)-math.Sin(latRad)*math.Sin(newLatRad))

	current.Latitude = newLatRad * (180.0 / math.Pi)
	current.Longitude = newLonRad * (180.0 / math.Pi)
	current.IsDeadReckoning = true

	return current
}

// evaluateDelay checks current timestamp against scheduled arrival at current/next station
func (e *RTISIngestionEngine) evaluateDelay(pt TelemetryPoint) int {
	timetable, exists := e.timetables[pt.TrainNumber]
	if !exists || len(timetable) == 0 {
		return pt.DelayMinutes
	}

	targetStation := pt.CurrentStationCode
	if targetStation == "" {
		targetStation = pt.NextStationCode
	}

	for _, stop := range timetable {
		if stop.StationCode == targetStation {
			if !stop.ScheduledArrival.IsZero() && pt.Timestamp.After(stop.ScheduledArrival) {
				delay := int(pt.Timestamp.Sub(stop.ScheduledArrival).Minutes())
				if delay > 0 {
					return delay
				}
			}
		}
	}
	return pt.DelayMinutes
}

// GetLatestTelemetry returns the most recently ingested transponder state for a train
func (e *RTISIngestionEngine) GetLatestTelemetry(trainNumber string) (TelemetryPoint, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	pt, ok := e.trainLatest[trainNumber]
	return pt, ok
}

// GetTelemetryHistory returns the recent trajectory coordinates for map rendering
func (e *RTISIngestionEngine) GetTelemetryHistory(trainNumber string) []TelemetryPoint {
	e.mu.RLock()
	defer e.mu.RUnlock()
	history := e.trainHistory[trainNumber]
	result := make([]TelemetryPoint, len(history))
	copy(result, history)
	return result
}

// GetStats returns telemetry ingestion metrics
func (e *RTISIngestionEngine) GetStats() IngestionStats {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.stats
}
