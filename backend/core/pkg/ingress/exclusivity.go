package ingress

import (
	"errors"
	"time"
)

var (
	ErrCitizenExclusivityWindow = errors.New("commercial aggregators and travel agents are barred during the 30-minute Citizen Exclusivity Tatkal opening")
)

type ExclusivityManager struct {
	timeProvider func() time.Time
}

func NewExclusivityManager(timeProvider func() time.Time) *ExclusivityManager {
	if timeProvider == nil {
		timeProvider = time.Now
	}
	return &ExclusivityManager{
		timeProvider: timeProvider,
	}
}

// IsCitizenExclusivityActive returns true during:
// 10:00:00 AM - 10:30:00 AM (AC Tatkal)
// 11:00:00 AM - 11:30:00 AM (Non-AC Tatkal)
func (m *ExclusivityManager) IsCitizenExclusivityActive(classCode string) bool {
	now := m.timeProvider()
	hour := now.Hour()
	minute := now.Minute()

	// 10:00 - 10:30 AM (AC Classes: 1A, 2A, 3A, 3E, CC, EC)
	if isACClass(classCode) {
		if hour == 10 && minute < 30 {
			return true
		}
	}

	// 11:00 - 11:30 AM (Non-AC Classes: SL, 2S)
	if !isACClass(classCode) {
		if hour == 11 && minute < 30 {
			return true
		}
	}

	return false
}

// CheckAccess validates if a given client type is permitted to book the requested class right now.
func (m *ExclusivityManager) CheckAccess(clientType ClientType, classCode string) error {
	if clientType == ClientCitizen {
		return nil // Genuine citizens are always permitted
	}

	if m.IsCitizenExclusivityActive(classCode) {
		return ErrCitizenExclusivityWindow
	}

	return nil
}

func isACClass(classCode string) bool {
	switch classCode {
	case "1A", "2A", "3A", "3E", "CC", "EC", "EA":
		return true
	default:
		return false
	}
}
