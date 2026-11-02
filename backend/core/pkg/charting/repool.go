package charting

import (
	"log"
	"time"
)

type QuotaSweeper struct{}

func NewQuotaSweeper() *QuotaSweeper {
	return &QuotaSweeper{}
}

// SweepUnutilizedQuotas extracts empty berths from VIP/HO, Defence, Foreign Tourist, etc.,
// and merges them into the General pool.
func (s *QuotaSweeper) SweepUnutilizedQuotas(run *ScheduledTrainRun) int {
	sweptTotal := 0

	specialQuotas := []QuotaCode{QuotaEmergencyHO, QuotaDefence, QuotaForeignTourist}
	for _, q := range specialQuotas {
		if unallocated, exists := run.QuotaPools[q]; exists && unallocated > 0 {
			sweptTotal += unallocated
			run.QuotaPools[QuotaGeneral] += unallocated
			run.QuotaPools[q] = 0
			log.Printf("[CHARTING] Swept %d unused berths from quota %s into General pool for train %s",
				unallocated, q, run.TrainNumber)
		}
	}

	return sweptTotal
}

// CascadeFreedBerths promotes RAC and Waitlist passengers into the newly freed berths.
func (s *QuotaSweeper) CascadeFreedBerths(run *ScheduledTrainRun) int {
	promotedCount := 0
	now := time.Now()

	// While General pool has available seats, absorb passengers
	for run.QuotaPools[QuotaGeneral] > 0 {
		if len(run.RACList) > 0 {
			// Promote RAC 1 to CNF
			promotedRAC := run.RACList[0]
			run.RACList = run.RACList[1:]

			promotedRAC.Status = StatusCNF
			promotedRAC.RACNumber = 0
			promotedRAC.AllocatedAt = &now
			run.ConfirmedList = append(run.ConfirmedList, promotedRAC)
			run.QuotaPools[QuotaGeneral]--
			promotedCount++

			// If WL exists, promote WL 1 to RAC to backfill RAC slot
			if len(run.Waitlist) > 0 {
				promotedWL := run.Waitlist[0]
				run.Waitlist = run.Waitlist[1:]

				promotedWL.Status = StatusRAC
				promotedWL.WLNumber = 0
				run.RACList = append(run.RACList, promotedWL)
			}
		} else if len(run.Waitlist) > 0 {
			// No RAC, but WL exists -> Promote WL directly to CNF
			promotedWL := run.Waitlist[0]
			run.Waitlist = run.Waitlist[1:]

			promotedWL.Status = StatusCNF
			promotedWL.WLNumber = 0
			promotedWL.AllocatedAt = &now
			run.ConfirmedList = append(run.ConfirmedList, promotedWL)
			run.QuotaPools[QuotaGeneral]--
			promotedCount++
		} else {
			// No more waiting passengers to absorb
			break
		}
	}

	// Renumber remaining queues
	for i, p := range run.RACList {
		p.RACNumber = i + 1
	}
	for i, p := range run.Waitlist {
		p.WLNumber = i + 1
	}

	return promotedCount
}
