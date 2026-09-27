package energy

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wyw14/solo-036/internal/model"
)

type Store struct {
	mu       sync.RWMutex
	byMeter  map[string][]model.MeterReading
	meterDev map[string]string
}

func NewStore() *Store {
	return &Store{byMeter: make(map[string][]model.MeterReading), meterDev: make(map[string]string)}
}

func (s *Store) Add(reading model.MeterReading) error {
	if strings.TrimSpace(reading.DeviceID) == "" {
		return errors.New("meter reading device is required")
	}
	if strings.TrimSpace(reading.MeterID) == "" {
		return errors.New("meter id is required")
	}
	if !reading.Kind.Valid() {
		return fmt.Errorf("invalid reading kind %q", reading.Kind)
	}
	if reading.KWh < 0 {
		return errors.New("meter reading must be non-negative")
	}
	if reading.At.IsZero() {
		return errors.New("meter reading time is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if device, ok := s.meterDev[reading.MeterID]; ok && device != reading.DeviceID {
		return fmt.Errorf("meter %s already belongs to device %s", reading.MeterID, device)
	}
	readings := s.byMeter[reading.MeterID]
	for _, existing := range readings {
		if existing.At.Equal(reading.At) {
			return fmt.Errorf("meter %s already has a reading at %s", reading.MeterID, reading.At.Format(time.RFC3339Nano))
		}
	}
	idx := sort.Search(len(readings), func(i int) bool { return readings[i].At.After(reading.At) })
	merged := make([]model.MeterReading, 0, len(readings)+1)
	merged = append(merged, readings[:idx]...)
	merged = append(merged, reading)
	merged = append(merged, readings[idx:]...)
	if err := validateOrder(merged); err != nil {
		return err
	}
	s.byMeter[reading.MeterID] = merged
	s.meterDev[reading.MeterID] = reading.DeviceID
	return nil
}

// validateOrder rejects regressions along the time-ordered readings of one
// meter unless the later reading explicitly opens a new baseline.
func validateOrder(readings []model.MeterReading) error {
	for i := 1; i < len(readings); i++ {
		previous, current := readings[i-1], readings[i]
		if current.Kind.StartsBaseline() {
			continue
		}
		if current.KWh < previous.KWh {
			return fmt.Errorf("meter %s reading %.3f at %s is below previous %.3f; record a replacement or reset reading instead",
				current.MeterID, current.KWh, current.At.Format(time.RFC3339Nano), previous.KWh)
		}
	}
	return nil
}

func (s *Store) Readings(deviceID string) []model.MeterReading {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.MeterReading, 0)
	for _, readings := range s.byMeter {
		for _, reading := range readings {
			if deviceID == "" || reading.DeviceID == deviceID {
				out = append(out, reading)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].At.Equal(out[j].At) {
			return out[i].ID < out[j].ID
		}
		return out[i].At.Before(out[j].At)
	})
	return out
}

func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	total := 0
	for _, readings := range s.byMeter {
		total += len(readings)
	}
	return total
}
