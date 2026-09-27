package model

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Meter is a cumulative energy meter (kWh dial) attached to one piece of
// equipment (for example station-a/compressor). Multiplier converts dial
// units to kWh (1 for a direct-connected meter, CT ratio otherwise).
type Meter struct {
	ID             string    `json:"id"`
	EquipmentID    string    `json:"equipment_id"`
	Multiplier     float64   `json:"multiplier"`
	InitialReading float64   `json:"initial_reading"`
	InstalledAt    time.Time `json:"installed_at"`
	RetiredAt      time.Time `json:"retired_at,omitempty"`
	Note           string    `json:"note,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

// Active reports whether the meter is still installed at instant t.
func (m Meter) Active(t time.Time) bool {
	if t.Before(m.InstalledAt) {
		return false
	}
	if !m.RetiredAt.IsZero() && t.After(m.RetiredAt) {
		return false
	}
	return true
}

func (m Meter) Validate() error {
	if strings.TrimSpace(m.ID) == "" {
		return errors.New("meter id is required")
	}
	if strings.TrimSpace(m.EquipmentID) == "" {
		return errors.New("meter equipment is required")
	}
	if m.Multiplier <= 0 {
		return errors.New("meter multiplier must be positive")
	}
	if m.InitialReading < 0 {
		return errors.New("meter initial reading must be non-negative")
	}
	if m.InstalledAt.IsZero() {
		return errors.New("meter installed_at is required")
	}
	if !m.RetiredAt.IsZero() && m.RetiredAt.Before(m.InstalledAt) {
		return errors.New("meter retired_at must not precede installed_at")
	}
	return nil
}

// MeterReading is one cumulative dial value observed at time At for one meter.
type MeterReading struct {
	ID          string    `json:"id"`
	MeterID     string    `json:"meter_id"`
	EquipmentID string    `json:"equipment_id"`
	Reading     float64   `json:"reading"`
	At          time.Time `json:"at"`
	RecordedAt  time.Time `json:"recorded_at"`
	Note        string    `json:"note,omitempty"`
}

func (r MeterReading) Validate() error {
	if strings.TrimSpace(r.ID) == "" {
		return errors.New("reading id is required")
	}
	if strings.TrimSpace(r.MeterID) == "" {
		return errors.New("reading meter id is required")
	}
	if strings.TrimSpace(r.EquipmentID) == "" {
		return errors.New("reading equipment id is required")
	}
	if r.Reading < 0 {
		return errors.New("meter reading must be non-negative")
	}
	if r.At.IsZero() {
		return errors.New("reading at is required")
	}
	return nil
}

// AnomalyKind classifies why part of the meter record cannot be allocated.
type AnomalyKind string

const (
	// AnomalyRollback means a later dial reading is below the previous one
	// (meter reset, reversed wiring or transcription error). The affected
	// delta cannot be trusted and is never charged to a run.
	AnomalyRollback AnomalyKind = "reading_rollback"
	// AnomalyCoverageGap means the equipment interval reaches past the last
	// known reading (or a meter was retired without a final reading). Energy
	// after the last reading is unknown and stays unattributed.
	AnomalyCoverageGap AnomalyKind = "coverage_gap"
	// AnomalyReplaceGap means no meter covered the equipment for part of the
	// reporting interval (for example the old meter was retired before the
	// replacement was installed).
	AnomalyReplaceGap AnomalyKind = "replacement_gap"
	// AnomalyDuplicateTime means two readings share the same timestamp, so
	// consumption between them cannot be placed on the time axis.
	AnomalyDuplicateTime AnomalyKind = "duplicate_timestamp"
)

// Anomaly records an auditable problem found while building segments.
type Anomaly struct {
	Kind        AnomalyKind `json:"kind"`
	EquipmentID string      `json:"equipment_id"`
	MeterID     string      `json:"meter_id,omitempty"`
	From        time.Time   `json:"from,omitempty"`
	To          time.Time   `json:"to,omitempty"`
	ReadingFrom float64     `json:"reading_from,omitempty"`
	ReadingTo   float64     `json:"reading_to,omitempty"`
	Message     string      `json:"message"`
}

// EnergySegment is the energy consumed by one meter between two consecutive
// dial readings. It is the atomic input to allocation and the evidence line
// behind every allocation share.
type EnergySegment struct {
	EquipmentID string    `json:"equipment_id"`
	MeterID     string    `json:"meter_id"`
	From        time.Time `json:"from"`
	To          time.Time `json:"to"`
	DialFrom    float64   `json:"dial_from"`
	DialTo      float64   `json:"dial_to"`
	Multiplier  float64   `json:"multiplier"`
	// Energy is (DialTo-DialFrom)*Multiplier in kWh; zero for rollback/
	// zero-duration segments.
	Energy float64 `json:"energy_kwh"`
	// Valid is false for rollback and duplicate-timestamp deltas: the segment
	// is reported for audit but its energy is not allocated.
	Valid bool `json:"valid"`
}

func (s EnergySegment) Duration() time.Duration { return s.To.Sub(s.From) }

// EnergyShare is the portion of one segment attributed to one run.
type EnergyShare struct {
	RunID       string    `json:"run_id"`
	SegmentFrom time.Time `json:"segment_from"`
	SegmentTo   time.Time `json:"segment_to"`
	MeterID     string    `json:"meter_id"`
	From        time.Time `json:"from"`
	To          time.Time `json:"to"`
	Energy      float64   `json:"energy_kwh"`
	// Basis explains the split: fraction of segment duration overlapping the
	// run window, e.g. 10m of 40m = 0.25 of 8.0 kWh.
	OverlapDuration time.Duration `json:"overlap_duration"`
	SegmentDuration time.Duration `json:"segment_duration"`
	Fraction        float64       `json:"fraction"`
}

// RunEnergy summarises allocation for one run.
type RunEnergy struct {
	RunID     string        `json:"run_id"`
	StationID string        `json:"station_id"`
	State     RunState      `json:"state"`
	From      time.Time     `json:"from"`
	To        time.Time     `json:"to"`
	Energy    float64       `json:"energy_kwh"`
	Shares    []EnergyShare `json:"shares"`
}

// EquipmentEnergy is the full allocation result for one piece of equipment.
type EquipmentEnergy struct {
	EquipmentID string          `json:"equipment_id"`
	From        time.Time       `json:"from"`
	To          time.Time       `json:"to"`
	Segments    []EnergySegment `json:"segments"`
	Runs        []RunEnergy     `json:"runs"`
	// UnattributedKWh is measured segment energy consumed while no run window
	// covered the equipment (idle / between jobs).
	UnattributedKWh float64 `json:"unattributed_kwh"`
	// UnknownKWh is energy that cannot be computed at all (rollback deltas,
	// coverage past the last reading, un-metered replacement gaps).
	UnknownKWh   float64   `json:"unknown_kwh"`
	AllocatedKWh float64   `json:"allocated_kwh"`
	Anomalies    []Anomaly `json:"anomalies"`
}

// EnergyReport bundles results for every equipment touched by the query.
type EnergyReport struct {
	From        time.Time         `json:"from"`
	To          time.Time         `json:"to"`
	GeneratedAt time.Time         `json:"generated_at"`
	Equipment   []EquipmentEnergy `json:"equipment"`
}

func (r EnergyReport) Totals() (allocated, unattributed, unknown float64) {
	for _, e := range r.Equipment {
		allocated += e.AllocatedKWh
		unattributed += e.UnattributedKWh
		unknown += e.UnknownKWh
	}
	return
}

// checkSegmentInvariant gives a human-readable basis line for audit output.
func (s EnergyShare) Basis() string {
	if s.SegmentDuration <= 0 {
		return fmt.Sprintf("meter %s zero-duration segment", s.MeterID)
	}
	return fmt.Sprintf("meter %s: %s overlap of %s = %.4f of segment",
		s.MeterID, s.OverlapDuration, s.SegmentDuration, s.Fraction)
}
