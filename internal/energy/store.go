package energy

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/wyw14/solo-036/internal/model"
)

// InstallMeter registers a new meter attached to equipment. A replacement
// meter must have its installation timestamp at or after the previous meter's
// retirement so coverage history stays unambiguous.
func (s *Store) InstallMeter(m model.Meter) error {
	if err := m.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.meters[m.ID]; exists {
		return fmt.Errorf("meter %s already registered", m.ID)
	}
	for _, existing := range s.meters {
		if existing.EquipmentID != m.EquipmentID {
			continue
		}
		if existing.RetiredAt.IsZero() {
			return fmt.Errorf("equipment %s already has active meter %s", m.EquipmentID, existing.ID)
		}
		if m.InstalledAt.Before(existing.RetiredAt) {
			return fmt.Errorf("meter %s installed before meter %s retired", m.ID, existing.ID)
		}
	}
	s.meters[m.ID] = m
	s.readings[m.ID] = make([]model.MeterReading, 0)
	return nil
}

// RetireMeter closes a meter's coverage window. Readings recorded before the
// retirement time remain valid for allocation.
func (s *Store) RetireMeter(id string, at time.Time) (model.Meter, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.meters[id]
	if !ok {
		return model.Meter{}, fmt.Errorf("meter %s not found", id)
	}
	if !m.RetiredAt.IsZero() {
		return model.Meter{}, fmt.Errorf("meter %s already retired at %s", id, m.RetiredAt.Format(time.RFC3339))
	}
	if at.Before(m.InstalledAt) {
		return model.Meter{}, errors.New("retirement time must not precede installation")
	}
	m.RetiredAt = at.UTC()
	s.meters[id] = m
	return m, nil
}

func (s *Store) GetMeter(id string) (model.Meter, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.meters[id]
	return m, ok
}

// Meters returns all meters sorted by equipment then installation time.
func (s *Store) Meters() []model.Meter {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.Meter, 0, len(s.meters))
	for _, m := range s.meters {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].EquipmentID != out[j].EquipmentID {
			return out[i].EquipmentID < out[j].EquipmentID
		}
		return out[i].InstalledAt.Before(out[j].InstalledAt)
	})
	return out
}

// MetersForEquipment returns the meter lineage for one equipment, oldest first.
func (s *Store) MetersForEquipment(equipmentID string) []model.Meter {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.Meter, 0)
	for _, m := range s.meters {
		if m.EquipmentID == equipmentID {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].InstalledAt.Before(out[j].InstalledAt) })
	return out
}

// AddReading stores a cumulative dial reading. Readings may be entered out of
// order; they are inserted by timestamp. Equal timestamps are allowed at
// storage time and flagged later, because operators do transcribe both sides of
// a meter change.
func (s *Store) AddReading(reading model.MeterReading) error {
	if err := reading.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.meters[reading.MeterID]
	if !ok {
		return fmt.Errorf("meter %s not found", reading.MeterID)
	}
	if m.EquipmentID != reading.EquipmentID {
		return fmt.Errorf("reading equipment %s does not match meter %s equipment %s", reading.EquipmentID, reading.MeterID, m.EquipmentID)
	}
	reading.At = reading.At.UTC()
	if reading.RecordedAt.IsZero() {
		reading.RecordedAt = time.Now().UTC()
	} else {
		reading.RecordedAt = reading.RecordedAt.UTC()
	}
	for _, existing := range s.readings[reading.MeterID] {
		if existing.ID == reading.ID {
			return fmt.Errorf("reading %s already recorded", reading.ID)
		}
	}
	s.readings[reading.MeterID] = insertReading(s.readings[reading.MeterID], reading)
	return nil
}

func insertReading(readings []model.MeterReading, reading model.MeterReading) []model.MeterReading {
	index := sort.Search(len(readings), func(i int) bool { return readings[i].At.After(reading.At) })
	readings = append(readings, model.MeterReading{})
	copy(readings[index+1:], readings[index:])
	readings[index] = reading
	return readings
}

// Readings returns readings for one meter in timestamp order.
func (s *Store) Readings(meterID string) []model.MeterReading {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]model.MeterReading(nil), s.readings[meterID]...)
}

// AllReadings returns every stored reading, ordered by equipment, meter, time.
func (s *Store) AllReadings() []model.MeterReading {
	s.mu.RLock()
	defer s.mu.RUnlock()
	meterIDs := make([]string, 0, len(s.readings))
	for id := range s.readings {
		meterIDs = append(meterIDs, id)
	}
	sort.Strings(meterIDs)
	out := make([]model.MeterReading, 0)
	for _, id := range meterIDs {
		out = append(out, s.readings[id]...)
	}
	return out
}

// Restore replaces all state from recovered snapshot data.
func (s *Store) Restore(meters []model.Meter, readings []model.MeterReading) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.meters = make(map[string]model.Meter, len(meters))
	s.readings = make(map[string][]model.MeterReading, len(meters))
	for _, m := range meters {
		if err := m.Validate(); err != nil {
			continue
		}
		s.meters[m.ID] = m
		s.readings[m.ID] = make([]model.MeterReading, 0)
	}
	for _, reading := range readings {
		if err := reading.Validate(); err != nil {
			continue
		}
		if _, ok := s.meters[reading.MeterID]; !ok {
			continue
		}
		reading.At = reading.At.UTC()
		s.readings[reading.MeterID] = insertReading(s.readings[reading.MeterID], reading)
	}
}
