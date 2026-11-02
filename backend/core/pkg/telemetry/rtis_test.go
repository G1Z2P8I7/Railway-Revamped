package telemetry

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestRTISIngestionThroughputAndDeadReckoning(t *testing.T) {
	engine := NewRTISIngestionEngine(10000, 100, 10*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var alertCount int64
	engine.RegisterAlertHandler(func(alert TransitAlert) {
		atomic.AddInt64(&alertCount, 1)
	})

	now := time.Now()
	// Register timetable: Train 12002 scheduled at NDLS at now
	engine.RegisterTimetable("12002", []TrainScheduleStop{
		{
			StationCode:      "NDLS",
			ScheduledArrival: now,
			DistanceKM:       0,
		},
		{
			StationCode:      "BPL",
			ScheduledArrival: now.Add(8 * time.Hour),
			DistanceKM:       700,
		},
	})

	engine.Start(ctx)

	// 1. Submit initial GPS fix with satellite lock
	p1 := TelemetryPoint{
		TrainNumber:        "12002",
		LocoID:             "WAP-7-30211",
		Timestamp:          now,
		Latitude:           28.6139,
		Longitude:          77.2090,
		SpeedKmph:          110.0,
		HeadingDegrees:     180.0, // heading South
		CurrentStationCode: "NDLS",
		NextStationCode:    "AGC",
		SignalAspect:       SignalGreen,
		SatelliteLock:      true,
	}

	if err := engine.SubmitTelemetry(p1); err != nil {
		t.Fatalf("failed to submit p1: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	latest, ok := engine.GetLatestTelemetry("12002")
	if !ok {
		t.Fatalf("expected latest telemetry for 12002")
	}
	if latest.Latitude != 28.6139 {
		t.Fatalf("expected lat 28.6139, got %f", latest.Latitude)
	}

	// 2. Submit second reading with lost satellite lock (train enters tunnel)
	p2 := TelemetryPoint{
		TrainNumber:        "12002",
		LocoID:             "WAP-7-30211",
		Timestamp:          now.Add(30 * time.Second),
		Latitude:           0, // No GPS fix
		Longitude:          0,
		SpeedKmph:          110.0,
		HeadingDegrees:     180.0,
		CurrentStationCode: "NDLS",
		NextStationCode:    "AGC",
		SignalAspect:       SignalGreen,
		SatelliteLock:      false, // Lost GPS lock
	}

	if err := engine.SubmitTelemetry(p2); err != nil {
		t.Fatalf("failed to submit p2: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	deadReckoned, ok := engine.GetLatestTelemetry("12002")
	if !ok {
		t.Fatalf("expected telemetry for 12002")
	}
	if !deadReckoned.IsDeadReckoning {
		t.Fatalf("expected IsDeadReckoning=true")
	}
	// Heading 180 degrees is South, so latitude should decrease from 28.6139
	if deadReckoned.Latitude >= 28.6139 {
		t.Fatalf("expected latitude to decrease Southbound, got %f", deadReckoned.Latitude)
	}
	if deadReckoned.Longitude == 0 {
		t.Fatalf("expected non-zero extrapolated longitude, got %f", deadReckoned.Longitude)
	}

	t.Logf("Dead-Reckoning verified: Extrapolated Lat=%f, Lon=%f (Heading South 180° at 110 km/h)",
		deadReckoned.Latitude, deadReckoned.Longitude)

	// 3. Test High-Throughput Burst Ingestion (Simulating ~500 writes/sec)
	startBurst := time.Now()
	numPoints := 2000
	for i := 0; i < numPoints; i++ {
		pt := TelemetryPoint{
			TrainNumber:        "12002",
			LocoID:             "WAP-7-30211",
			Timestamp:          now.Add(time.Duration(i) * time.Second),
			Latitude:           28.6139 - float64(i)*0.0001,
			Longitude:          77.2090,
			SpeedKmph:          120.0,
			HeadingDegrees:     180.0,
			CurrentStationCode: "NDLS",
			SatelliteLock:      true,
		}
		if err := engine.SubmitTelemetry(pt); err != nil {
			t.Fatalf("burst submission failed at %d: %v", i, err)
		}
	}

	elapsed := time.Since(startBurst)
	t.Logf("Ingested %d transponder points in %v (Rate: %.0f writes/second; Target: 467/s)",
		numPoints, elapsed, float64(numPoints)/elapsed.Seconds())

	// Wait for queue flush
	time.Sleep(150 * time.Millisecond)

	stats := engine.GetStats()
	if stats.TotalIngested < uint64(numPoints) {
		t.Fatalf("expected at least %d ingested, got %d", numPoints, stats.TotalIngested)
	}

	// 4. Test Automated Delay Alert Triggering (>30 min delay)
	pDelay := TelemetryPoint{
		TrainNumber:        "12002",
		LocoID:             "WAP-7-30211",
		Timestamp:          now.Add(45 * time.Minute), // Arrived 45 min late at NDLS
		Latitude:           28.6139,
		Longitude:          77.2090,
		SpeedKmph:          0,
		CurrentStationCode: "NDLS",
		SatelliteLock:      true,
	}

	if err := engine.SubmitTelemetry(pDelay); err != nil {
		t.Fatalf("failed to submit delay point: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	latest, _ = engine.GetLatestTelemetry("12002")
	if latest.DelayMinutes < 40 {
		t.Fatalf("expected delay >= 40 mins, got %d", latest.DelayMinutes)
	}

	time.Sleep(50 * time.Millisecond)
	if atomic.LoadInt64(&alertCount) == 0 {
		t.Fatalf("expected Major Delay alert to be emitted")
	}

	t.Logf("Automated Transit Delay Alert verified: %d min delay detected against scheduled timetable",
		latest.DelayMinutes)
}
