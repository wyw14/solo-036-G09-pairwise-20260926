package service

import (
	"testing"
	"time"

	"github.com/wyw14/solo-036/internal/model"
	"github.com/wyw14/solo-036/internal/run"
)

func restoreRun(t *testing.T, rt *Runtime, id string, start, end time.Time, state model.RunState) {
	t.Helper()
	run.Restore(rt.Runs, []model.Run{{
		ID:        id,
		Station:   "station-a",
		State:     state,
		Epoch:     7,
		CreatedAt: start,
		UpdatedAt: end,
	}})
}

func TestEnergyReportAllocatesToRun(t *testing.T) {
	rt := NewRuntime()
	from := time.Date(2026, 9, 10, 8, 0, 0, 0, time.UTC)
	end := from.Add(40 * time.Minute)
	restoreRun(t, rt, "run-1", from, end, model.StateComplete)

	meter, err := rt.InstallMeter(InstallMeterRequest{
		ID:             "m-1",
		EquipmentID:    "station-a/compressor",
		Multiplier:     1,
		InitialReading: 100,
		InstalledAt:    from.Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("install meter: %v", err)
	}
	if _, err := rt.AddMeterReading(MeterReadingRequest{
		MeterID: meter.ID, Reading: 108, At: end.Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("add reading: %v", err)
	}

	report := rt.EnergyReport(from, end, nil)
	if len(report.Equipment) != 1 {
		t.Fatalf("want one equipment statement, got %d", len(report.Equipment))
	}
	statement := report.Equipment[0]
	if len(statement.Runs) != 1 || statement.Runs[0].RunID != "run-1" {
		t.Fatalf("want run-1 allocation, got %+v", statement.Runs)
	}
	if d := statement.Runs[0].Energy - 8; d > 1e-6 || d < -1e-6 {
		t.Fatalf("run energy want 8 kWh got %.4f", statement.Runs[0].Energy)
	}
	if statement.UnattributedKWh != 0 {
		t.Fatalf("want no unattributed energy, got %.4f", statement.UnattributedKWh)
	}
	allocated, unattributed, unknown := report.Totals()
	if allocated != 8 || unattributed != 0 || unknown != 0 {
		t.Fatalf("totals want 8/0/0 got %.4f/%.4f/%.4f", allocated, unattributed, unknown)
	}

	// The same numbers are reachable per run.
	runEnergy, err := rt.RunEnergy("run-1", from, end)
	if err != nil {
		t.Fatalf("run energy: %v", err)
	}
	if runEnergy.Energy != 8 || len(runEnergy.Shares) != 1 {
		t.Fatalf("per-run statement mismatch: %+v", runEnergy)
	}
}

func TestMeterReadingRollbackRaisesIncident(t *testing.T) {
	rt := NewRuntime()
	from := time.Date(2026, 9, 10, 8, 0, 0, 0, time.UTC)
	if _, err := rt.InstallMeter(InstallMeterRequest{
		ID: "m-1", EquipmentID: "station-a/compressor", InstalledAt: from.Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.AddMeterReading(MeterReadingRequest{MeterID: "m-1", Reading: 50, At: from.Add(10 * time.Minute).Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.AddMeterReading(MeterReadingRequest{MeterID: "m-1", Reading: 40, At: from.Add(20 * time.Minute).Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}

	incidents := rt.Snapshot().Incidents
	found := false
	for _, incident := range incidents {
		if incident.Severity == "warning" && incident.Message != "" {
			found = true
		}
	}
	if !found {
		t.Fatalf("rollback must record a warning incident, got %+v", incidents)
	}

	statement := rt.EnergyReport(from, from.Add(30*time.Minute), nil).Equipment
	if len(statement) != 1 || !containsAnomaly(statement[0].Anomalies, model.AnomalyRollback) {
		t.Fatalf("rollback must surface in report anomalies: %+v", statement)
	}
}

func TestEnergyRejectsReadingForInactiveMeter(t *testing.T) {
	rt := NewRuntime()
	from := time.Date(2026, 9, 10, 8, 0, 0, 0, time.UTC)
	if _, err := rt.InstallMeter(InstallMeterRequest{
		ID: "m-1", EquipmentID: "station-a/compressor", InstalledAt: from.Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.RetireMeter("m-1", RetireMeterRequest{RetiredAt: from.Add(time.Hour).Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.AddMeterReading(MeterReadingRequest{
		MeterID: "m-1", Reading: 9, At: from.Add(2 * time.Hour).Format(time.RFC3339),
	}); err == nil {
		t.Fatal("reading after retirement must be rejected")
	}
}

func TestEnergySnapshotRoundTrip(t *testing.T) {
	rt := NewRuntime()
	from := time.Date(2026, 9, 10, 8, 0, 0, 0, time.UTC)
	if _, err := rt.InstallMeter(InstallMeterRequest{
		ID: "m-1", EquipmentID: "station-a/compressor", Multiplier: 2,
		InitialReading: 5, InstalledAt: from.Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.AddMeterReading(MeterReadingRequest{MeterID: "m-1", Reading: 9, At: from.Add(30 * time.Minute).Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}
	snapshot := rt.Snapshot()
	if err := snapshot.Validate(); err != nil {
		t.Fatalf("snapshot with meters must validate: %v", err)
	}

	recovered := NewRuntime()
	recovered.Meters.Restore(snapshot.Meters, snapshot.MeterReadings)
	meters := recovered.ListMeters()
	if len(meters) != 1 || meters[0].Multiplier != 2 {
		t.Fatalf("meter not restored: %+v", meters)
	}
	if readings := recovered.ListMeterReadings(); len(readings) != 2 {
		t.Fatalf("want initial + manual reading restored, got %d", len(readings))
	}
}

func containsAnomaly(anomalies []model.Anomaly, kind model.AnomalyKind) bool {
	for _, a := range anomalies {
		if a.Kind == kind {
			return true
		}
	}
	return false
}
