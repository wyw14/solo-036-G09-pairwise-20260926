package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wyw14/solo-036/internal/energy"
	"github.com/wyw14/solo-036/internal/model"
)

type ReadingRequest struct {
	DeviceID string
	MeterID  string
	KWh      float64
	Kind     model.ReadingKind
	Note     string
	At       time.Time
}

// meteredDevice reports whether deviceID names a compressor at a known
// station, e.g. "station-a/compressor".
func (r *Runtime) meteredDevice(deviceID string) (string, bool) {
	stationID, ok := strings.CutSuffix(deviceID, "/compressor")
	if !ok {
		return "", false
	}
	control := r.ensureControl()
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok = control.Stations[stationID]
	return stationID, ok
}

func (r *Runtime) RecordReading(request ReadingRequest) (model.MeterReading, error) {
	if _, ok := r.meteredDevice(request.DeviceID); !ok {
		return model.MeterReading{}, fmt.Errorf("unknown metered device %s", request.DeviceID)
	}
	kind := request.Kind
	if kind == "" {
		kind = model.ReadingNormal
	}
	now := time.Now().UTC()
	reading := model.MeterReading{
		ID:         model.NewRevision(),
		DeviceID:   request.DeviceID,
		MeterID:    strings.TrimSpace(request.MeterID),
		KWh:        request.KWh,
		Kind:       kind,
		Note:       strings.TrimSpace(request.Note),
		At:         request.At,
		RecordedAt: now,
	}
	if reading.At.IsZero() {
		reading.At = now
	}
	if err := r.Energy.Add(reading); err != nil {
		return model.MeterReading{}, err
	}
	payload, err := json.Marshal(reading)
	if err != nil {
		return model.MeterReading{}, fmt.Errorf("marshal meter reading: %w", err)
	}
	if err := r.appendEvent(model.Event{
		ID:      reading.ID,
		Kind:    energy.MeterReadingEventKind,
		Payload: string(payload),
		At:      now,
	}); err != nil {
		return model.MeterReading{}, err
	}
	return reading, nil
}

func (r *Runtime) MeterReadings(deviceID string) []model.MeterReading {
	return r.Energy.Readings(deviceID)
}

func (r *Runtime) EnergyAllocation(deviceID string, from, to time.Time) (model.EnergyReport, error) {
	stationID, ok := r.meteredDevice(deviceID)
	if !ok {
		return model.EnergyReport{}, fmt.Errorf("unknown metered device %s", deviceID)
	}
	if to.IsZero() {
		to = time.Now().UTC()
	}
	if !from.IsZero() && !from.Before(to) {
		return model.EnergyReport{}, errors.New("from must be before to")
	}
	now := time.Now().UTC()
	windows := make([]energy.RunWindow, 0)
	for _, run := range r.Runs.Snapshot() {
		if run.Station != stationID {
			continue
		}
		end := run.UpdatedAt
		if !run.State.Terminal() {
			end = now
		}
		if end.Before(run.CreatedAt) {
			end = run.CreatedAt
		}
		windows = append(windows, energy.RunWindow{RunID: run.ID, Start: run.CreatedAt, End: end})
	}
	return energy.Allocate(deviceID, r.Energy.Readings(deviceID), windows, from, to), nil
}

func (r *Runtime) RunEnergy(runID string) (model.RunAllocation, error) {
	run, ok := r.Runs.Get(runID)
	if !ok {
		return model.RunAllocation{}, fmt.Errorf("run %s not found", runID)
	}
	report, err := r.EnergyAllocation(run.Station+"/compressor", time.Time{}, time.Now().UTC())
	if err != nil {
		return model.RunAllocation{}, err
	}
	for _, allocation := range report.Runs {
		if allocation.RunID == runID {
			return allocation, nil
		}
	}
	return model.RunAllocation{RunID: runID, Basis: []model.AllocationBasis{}}, nil
}

// restoreReadings replays journaled meter readings after recovery.
func (r *Runtime) restoreReadings(events []model.Event) int {
	return energy.Restore(r.Energy, events)
}
