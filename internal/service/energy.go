package service

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wyw14/solo-036/internal/energy"
	"github.com/wyw14/solo-036/internal/model"
)

type InstallMeterRequest struct {
	ID             string  `json:"meter_id"`
	EquipmentID    string  `json:"equipment_id"`
	Multiplier     float64 `json:"multiplier"`
	InitialReading float64 `json:"initial_reading"`
	InstalledAt    string  `json:"installed_at"`
	Note           string  `json:"note"`
}

type MeterReadingRequest struct {
	MeterID     string  `json:"meter_id"`
	EquipmentID string  `json:"equipment_id"`
	Reading     float64 `json:"reading"`
	At          string  `json:"at"`
	Note        string  `json:"note"`
}

type RetireMeterRequest struct {
	RetiredAt string `json:"retired_at"`
}

// compressorEquipment maps a run to the compressor meter equipment at its
// launch station. Equipment naming matches EquipmentStatus
// ("<station>/compressor").
func compressorEquipment(stationID string) string { return stationID + "/compressor" }

func (r *Runtime) InstallMeter(request InstallMeterRequest) (model.Meter, error) {
	if strings.TrimSpace(request.ID) == "" {
		return model.Meter{}, errors.New("meter_id is required")
	}
	if strings.TrimSpace(request.EquipmentID) == "" {
		return model.Meter{}, errors.New("equipment_id is required")
	}
	installedAt, err := parseEnergyTime(request.InstalledAt)
	if err != nil {
		return model.Meter{}, err
	}
	if request.Multiplier == 0 {
		request.Multiplier = 1
	}
	meter := model.Meter{
		ID:             strings.TrimSpace(request.ID),
		EquipmentID:    strings.TrimSpace(request.EquipmentID),
		Multiplier:     request.Multiplier,
		InitialReading: request.InitialReading,
		InstalledAt:    installedAt,
		Note:           strings.TrimSpace(request.Note),
		CreatedAt:      time.Now().UTC(),
	}
	if err := r.Meters.InstallMeter(meter); err != nil {
		return model.Meter{}, err
	}
	// Seed the dial baseline at installation so consumption between install
	// and the first manual reading is measurable and auditable.
	if err := r.Meters.AddReading(model.MeterReading{
		ID:          meter.ID + "/initial",
		MeterID:     meter.ID,
		EquipmentID: meter.EquipmentID,
		Reading:     meter.InitialReading,
		At:          meter.InstalledAt,
		RecordedAt:  meter.CreatedAt,
		Note:        "initial reading at installation",
	}); err != nil {
		return model.Meter{}, fmt.Errorf("seed initial reading: %w", err)
	}
	if err := r.appendEvent(model.Event{
		Kind:    "meter_registered",
		Epoch:   installedAt.UnixNano(),
		Payload: fmt.Sprintf("%s@%s x%.4f start %.3f", meter.ID, meter.EquipmentID, meter.Multiplier, meter.InitialReading),
		At:      time.Now().UTC(),
	}); err != nil {
		return model.Meter{}, err
	}
	r.bumpRevision()
	return meter, nil
}

func (r *Runtime) RetireMeter(meterID string, request RetireMeterRequest) (model.Meter, error) {
	retiredAt, err := parseEnergyTime(request.RetiredAt)
	if err != nil {
		return model.Meter{}, err
	}
	meter, err := r.Meters.RetireMeter(strings.TrimSpace(meterID), retiredAt)
	if err != nil {
		return model.Meter{}, err
	}
	if err := r.appendEvent(model.Event{
		Kind:    "meter_retired",
		Epoch:   retiredAt.UnixNano(),
		Payload: fmt.Sprintf("%s@%s", meter.ID, meter.EquipmentID),
		At:      time.Now().UTC(),
	}); err != nil {
		return model.Meter{}, err
	}
	r.bumpRevision()
	return meter, nil
}

func (r *Runtime) AddMeterReading(request MeterReadingRequest) (model.MeterReading, error) {
	meterID := strings.TrimSpace(request.MeterID)
	equipmentID := strings.TrimSpace(request.EquipmentID)
	meter, ok := r.Meters.GetMeter(meterID)
	if !ok {
		return model.MeterReading{}, fmt.Errorf("meter %s not found", meterID)
	}
	if equipmentID == "" {
		equipmentID = meter.EquipmentID
	}
	observedAt, err := parseEnergyTime(request.At)
	if err != nil {
		return model.MeterReading{}, err
	}
	if !meter.Active(observedAt) {
		return model.MeterReading{}, fmt.Errorf("meter %s was not in service at %s", meterID, observedAt.Format(time.RFC3339))
	}
	previous := previousDialAt(r.Meters.Readings(meterID), observedAt)
	reading := model.MeterReading{
		ID:          model.NewRevision(),
		MeterID:     meterID,
		EquipmentID: equipmentID,
		Reading:     request.Reading,
		At:          observedAt,
		RecordedAt:  time.Now().UTC(),
		Note:        strings.TrimSpace(request.Note),
	}
	if err := r.Meters.AddReading(reading); err != nil {
		return model.MeterReading{}, err
	}
	if previous != nil {
		switch {
		case request.Reading+1e-9 < previous.Reading:
			r.recordIncident("", "warning", fmt.Sprintf(
				"meter %s reading rolled back from %.3f to %.3f at %s; delta excluded from allocation",
				meterID, previous.Reading, request.Reading, observedAt.Format(time.RFC3339)))
		case request.Reading == previous.Reading:
			r.recordIncident("", "info", fmt.Sprintf(
				"meter %s repeated reading %.3f at %s", meterID, request.Reading, observedAt.Format(time.RFC3339)))
		}
	}
	if err := r.appendEvent(model.Event{
		Kind:    "meter_reading",
		Epoch:   observedAt.UnixNano(),
		Payload: fmt.Sprintf("%s@%s %.3f", meterID, equipmentID, request.Reading),
		At:      reading.RecordedAt,
	}); err != nil {
		return model.MeterReading{}, err
	}
	r.bumpRevision()
	return reading, nil
}

// previousDialAt returns the latest reading strictly before t (the dial base
// for rollback checks).
func previousDialAt(readings []model.MeterReading, t time.Time) *model.MeterReading {
	var previous *model.MeterReading
	for i := range readings {
		if readings[i].At.Before(t) {
			previous = &readings[i]
		}
	}
	return previous
}

func (r *Runtime) ListMeters() []model.Meter               { return r.Meters.Meters() }
func (r *Runtime) ListMeterReadings() []model.MeterReading { return r.Meters.AllReadings() }

// runWindowsByEquipment derives energy windows from run history. Terminal
// runs close at their last transition time; active runs stay open and are
// clipped by the report's end time during allocation.
func (r *Runtime) runWindowsByEquipment(from, to time.Time) map[string][]energy.RunWindow {
	windows := make(map[string][]energy.RunWindow)
	for _, record := range r.Runs.Snapshot() {
		start := record.CreatedAt.UTC()
		end := to
		if record.State.Terminal() {
			end = record.UpdatedAt.UTC()
		}
		if !end.After(from) || !to.After(start) {
			continue
		}
		equipmentID := compressorEquipment(record.Station)
		windows[equipmentID] = append(windows[equipmentID], energy.RunWindow{
			RunID:     record.ID,
			StationID: record.Station,
			State:     record.State,
			Start:     start,
			End:       end,
		})
	}
	return windows
}

// EnergyReport allocates measured compressor energy between from and to.
// When equipmentIDs is empty the report covers every equipment with a meter.
func (r *Runtime) EnergyReport(from, to time.Time, equipmentIDs []string) model.EnergyReport {
	from, to = from.UTC(), to.UTC()
	if to.IsZero() {
		to = time.Now().UTC()
	}
	if from.IsZero() {
		from = to.Add(-24 * time.Hour)
	}
	if !to.After(from) {
		return model.EnergyReport{From: from, To: to, GeneratedAt: time.Now().UTC(), Equipment: []model.EquipmentEnergy{}}
	}
	if len(equipmentIDs) == 0 {
		for _, meter := range r.Meters.Meters() {
			equipmentIDs = append(equipmentIDs, meter.EquipmentID)
		}
	}
	windows := r.runWindowsByEquipment(from, to)
	return energy.Report(r.Meters, equipmentIDs, windows, from, to)
}

// RunEnergy extracts one run's allocation from the equipment it consumed.
func (r *Runtime) RunEnergy(runID string, from, to time.Time) (model.RunEnergy, error) {
	record, ok := r.Runs.Get(runID)
	if !ok {
		return model.RunEnergy{}, fmt.Errorf("run %s not found", runID)
	}
	if from.IsZero() {
		from = record.CreatedAt.Add(-time.Hour).UTC()
	}
	if to.IsZero() {
		to = time.Now().UTC()
	}
	report := r.EnergyReport(from, to, []string{compressorEquipment(record.Station)})
	for _, equipment := range report.Equipment {
		for _, runEnergy := range equipment.Runs {
			if runEnergy.RunID == runID {
				return runEnergy, nil
			}
		}
	}
	return model.RunEnergy{
		RunID:     runID,
		StationID: record.Station,
		State:     record.State,
		From:      record.CreatedAt.UTC(),
		To:        runWindowEnd(record, to),
		Energy:    0,
		Shares:    []model.EnergyShare{},
	}, nil
}

// runWindowEnd closes terminal runs at their last transition and leaves
// active runs open (zero time), to be clipped by the report end.
func runWindowEnd(record model.Run, reportEnd time.Time) time.Time {
	if record.State.Terminal() {
		return record.UpdatedAt.UTC()
	}
	return reportEnd
}

func parseEnergyTime(value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Now().UTC(), nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}, errors.New("time must use RFC3339 format")
	}
	return parsed.UTC(), nil
}

func (r *Runtime) bumpRevision() {
	r.mu.Lock()
	if r.control == nil {
		r.control = defaultControlState()
	}
	r.control.Revision = model.NewRevision()
	r.mu.Unlock()
}
