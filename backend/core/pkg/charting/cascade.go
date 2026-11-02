package charting

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrPassengerNotFound = errors.New("passenger not found on train manifest")
	ErrAlreadyCancelled  = errors.New("ticket passenger is already cancelled")
)

type CascadeEngine struct {
	mu sync.Mutex
}

func NewCascadeEngine() *CascadeEngine {
	return &CascadeEngine{}
}

// HandleCancellation executes deterministic FIFO cascade:
// If CNF cancels: RAC 1 -> CNF, WL 1 -> RAC (or WL 1 -> CNF if RAC is empty).
func (e *CascadeEngine) HandleCancellation(run *ScheduledTrainRun, pnrNumber, passengerID string) (*TicketPassenger, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// 1. Locate the passenger in Confirmed, RAC, or WL
	var target *TicketPassenger
	targetIndex := -1
	targetListType := "" // "CNF", "RAC", "WL"

	for i, p := range run.ConfirmedList {
		if p.PNRNumber == pnrNumber && p.PassengerID == passengerID {
			target = p
			targetIndex = i
			targetListType = "CNF"
			break
		}
	}

	if target == nil {
		for i, p := range run.RACList {
			if p.PNRNumber == pnrNumber && p.PassengerID == passengerID {
				target = p
				targetIndex = i
				targetListType = "RAC"
				break
			}
		}
	}

	if target == nil {
		for i, p := range run.Waitlist {
			if p.PNRNumber == pnrNumber && p.PassengerID == passengerID {
				target = p
				targetIndex = i
				targetListType = "WL"
				break
			}
		}
	}

	if target == nil {
		return nil, ErrPassengerNotFound
	}

	if target.Status == StatusCAN {
		return nil, ErrAlreadyCancelled
	}

	target.Status = StatusCAN

	now := time.Now()

	// 2. Cascade logic based on list type
	switch targetListType {
	case "CNF":
		// Remove cancelled passenger from ConfirmedList
		run.ConfirmedList = append(run.ConfirmedList[:targetIndex], run.ConfirmedList[targetIndex+1:]...)

		// Case A: RAC exists -> Promote RAC 1 to CNF
		if len(run.RACList) > 0 {
			promotedRAC := run.RACList[0]
			run.RACList = run.RACList[1:]

			promotedRAC.Status = StatusCNF
			promotedRAC.RACNumber = 0
			promotedRAC.AllocatedAt = &now
			run.ConfirmedList = append(run.ConfirmedList, promotedRAC)

			// Promote WL 1 to RAC
			if len(run.Waitlist) > 0 {
				promotedWL := run.Waitlist[0]
				run.Waitlist = run.Waitlist[1:]

				promotedWL.Status = StatusRAC
				promotedWL.WLNumber = 0
				run.RACList = append(run.RACList, promotedWL)
			}
		} else if len(run.Waitlist) > 0 {
			// Case B: No RAC, but WL exists -> Promote WL 1 directly to CNF
			promotedWL := run.Waitlist[0]
			run.Waitlist = run.Waitlist[1:]

			promotedWL.Status = StatusCNF
			promotedWL.WLNumber = 0
			promotedWL.AllocatedAt = &now
			run.ConfirmedList = append(run.ConfirmedList, promotedWL)
		} else {
			// Case C: Neither RAC nor WL exists -> Return 1 berth back to General Pool
			run.QuotaPools[QuotaGeneral]++
		}

	case "RAC":
		// Remove cancelled passenger from RACList
		run.RACList = append(run.RACList[:targetIndex], run.RACList[targetIndex+1:]...)

		// Promote WL 1 to RAC
		if len(run.Waitlist) > 0 {
			promotedWL := run.Waitlist[0]
			run.Waitlist = run.Waitlist[1:]

			promotedWL.Status = StatusRAC
			promotedWL.WLNumber = 0
			run.RACList = append(run.RACList, promotedWL)
		}

	case "WL":
		// Remove cancelled passenger from Waitlist
		run.Waitlist = append(run.Waitlist[:targetIndex], run.Waitlist[targetIndex+1:]...)
	}

	// 3. Renumber remaining queues in strict chronological order
	e.renumberQueuesLocked(run)

	return target, nil
}

func (e *CascadeEngine) renumberQueuesLocked(run *ScheduledTrainRun) {
	for i, p := range run.RACList {
		p.RACNumber = i + 1
	}
	for i, p := range run.Waitlist {
		p.WLNumber = i + 1
	}
}

// GetManifestSummary returns real-time count breakdown of CNF, RAC, and WL.
func (e *CascadeEngine) GetManifestSummary(run *ScheduledTrainRun) string {
	return fmt.Sprintf("Confirmed: %d | RAC: %d | WL: %d | Available General Berths: %d",
		len(run.ConfirmedList), len(run.RACList), len(run.Waitlist), run.QuotaPools[QuotaGeneral])
}
