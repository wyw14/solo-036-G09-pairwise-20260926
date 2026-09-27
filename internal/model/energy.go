package model

import "time"

type ReadingKind string

const (
	ReadingNormal      ReadingKind = "reading"
	ReadingReplacement ReadingKind = "replacement"
	ReadingReset       ReadingKind = "reset"
)

func (k ReadingKind) Valid() bool {
	switch k {
	case ReadingNormal, ReadingReplacement, ReadingReset:
		return true
	}
	return false
}

// StartsBaseline reports whether the reading opens a new meter baseline,
// making the interval before it unmeasurable.
func (k ReadingKind) StartsBaseline() bool {
	return k == ReadingReplacement || k == ReadingReset
}

type MeterReading struct {
	ID         string      `json:"id"`
	DeviceID   string      `json:"device_id"`
	MeterID    string      `json:"meter_id"`
	KWh        float64     `json:"kwh"`
	Kind       ReadingKind `json:"kind"`
	Note       string      `json:"note,omitempty"`
	At         time.Time   `json:"at"`
	RecordedAt time.Time   `json:"recorded_at"`
}

type MeterGap struct {
	MeterID string    `json:"meter_id"`
	FromAt  time.Time `json:"from_at"`
	ToAt    time.Time `json:"to_at"`
	Reason  string    `json:"reason"`
}

type AllocationBasis struct {
	MeterID        string    `json:"meter_id"`
	FromReadingID  string    `json:"from_reading_id"`
	ToReadingID    string    `json:"to_reading_id"`
	IntervalStart  time.Time `json:"interval_start"`
	IntervalEnd    time.Time `json:"interval_end"`
	IntervalKWh    float64   `json:"interval_kwh"`
	OverlapSeconds float64   `json:"overlap_seconds"`
	Share          float64   `json:"share"`
	KWh            float64   `json:"kwh"`
}

type RunAllocation struct {
	RunID       string            `json:"run_id"`
	WindowStart time.Time         `json:"window_start"`
	WindowEnd   time.Time         `json:"window_end"`
	KWh         float64           `json:"kwh"`
	Basis       []AllocationBasis `json:"basis"`
}

type EnergyReport struct {
	DeviceID       string          `json:"device_id"`
	From           time.Time       `json:"from"`
	To             time.Time       `json:"to"`
	MeasuredKWh    float64         `json:"measured_kwh"`
	AllocatedKWh   float64         `json:"allocated_kwh"`
	UnallocatedKWh float64         `json:"unallocated_kwh"`
	Gaps           []MeterGap      `json:"gaps"`
	Runs           []RunAllocation `json:"runs"`
}
