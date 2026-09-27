package energy

import (
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/wyw14/solo-036/internal/model"
)

func trimFloat(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

var baseTime = time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)

func minute(n float64) time.Duration {
	return time.Duration(n * float64(time.Minute))
}

func newTestStore(t *testing.T, meter model.Meter) *Store {
	t.Helper()
	store := NewStore()
	if err := store.InstallMeter(meter); err != nil {
		t.Fatalf("install meter: %v", err)
	}
	return store
}

func addReading(t *testing.T, store *Store, meterID, equipmentID string, value float64, at time.Time) {
	t.Helper()
	err := store.AddReading(model.MeterReading{
		ID:          "r-" + at.Format("150405.000") + "-" + meterID + "-" + trimFloat(value),
		MeterID:     meterID,
		EquipmentID: equipmentID,
		Reading:     value,
		At:          at,
		RecordedAt:  at,
	})
	if err != nil {
		t.Fatalf("add reading: %v", err)
	}
}

func testMeter(id, equipment string, multiplier float64, installed, retired time.Time) model.Meter {
	return model.Meter{
		ID:          id,
		EquipmentID: equipment,
		Multiplier:  multiplier,
		InstalledAt: installed,
		CreatedAt:   installed,
		RetiredAt:   retired,
	}
}

func window(runID string, start, end time.Time) RunWindow {
	return RunWindow{RunID: runID, StationID: "station-a", State: model.StateComplete, Start: start, End: end}
}

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func findRun(t *testing.T, runs []model.RunEnergy, runID string) model.RunEnergy {
	t.Helper()
	for _, r := range runs {
		if r.RunID == runID {
			return r
		}
	}
	t.Fatalf("run %s not in allocation", runID)
	return model.RunEnergy{}
}

func hasAnomaly(anomalies []model.Anomaly, kind model.AnomalyKind) bool {
	for _, a := range anomalies {
		if a.Kind == kind {
			return true
		}
	}
	return false
}

// A single run covering the whole segment receives all measured energy and
// every share carries the audit basis (fraction, overlap, meter id).
func TestAllocateSingleRun(t *testing.T) {
	const eq = "station-a/compressor"
	store := newTestStore(t, testMeter("m1", eq, 1, baseTime, time.Time{}))
	addReading(t, store, "m1", eq, 100, baseTime)
	addReading(t, store, "m1", eq, 108, baseTime.Add(minute(40)))

	report := EquipmentReport(store, eq, baseTime, baseTime.Add(minute(40)),
		[]RunWindow{window("run-1", baseTime, baseTime.Add(minute(40)))})

	if len(report.Segments) != 1 || !approx(report.Segments[0].Energy, 8) {
		t.Fatalf("want one 8 kWh segment, got %+v", report.Segments)
	}
	if len(report.Runs) != 1 || !approx(report.Runs[0].Energy, 8) {
		t.Fatalf("want run allocated 8 kWh, got %+v", report.Runs)
	}
	if !approx(report.UnattributedKWh, 0) || !approx(report.AllocatedKWh, 8) {
		t.Fatalf("want zero unattributed, got unattr=%.4f alloc=%.4f", report.UnattributedKWh, report.AllocatedKWh)
	}
	share := report.Runs[0].Shares[0]
	if share.MeterID != "m1" || !approx(share.Fraction, 1) || share.OverlapDuration != minute(40) {
		t.Fatalf("share basis not auditable: %+v", share)
	}
}

// Energy measured while no run window covers the equipment is unattributed,
// not silently dropped or charged to a job.
func TestAllocateIdleIsUnattributed(t *testing.T) {
	const eq = "station-a/compressor"
	store := newTestStore(t, testMeter("m1", eq, 1, baseTime, time.Time{}))
	addReading(t, store, "m1", eq, 100, baseTime)
	addReading(t, store, "m1", eq, 108, baseTime.Add(minute(40)))

	report := EquipmentReport(store, eq, baseTime, baseTime.Add(minute(40)), nil)

	if !approx(report.UnattributedKWh, 8) || !approx(report.AllocatedKWh, 0) {
		t.Fatalf("want 8 unattributed / 0 allocated, got unattr=%.4f alloc=%.4f", report.UnattributedKWh, report.AllocatedKWh)
	}
}

// Time-linear split across one run, an idle gap and a second run, including a
// partial overlap of a run with the segment.
func TestAllocateProportionalAcrossRuns(t *testing.T) {
	const eq = "station-a/compressor"
	store := newTestStore(t, testMeter("m1", eq, 1, baseTime, time.Time{}))
	addReading(t, store, "m1", eq, 100, baseTime)
	addReading(t, store, "m1", eq, 108, baseTime.Add(minute(40)))

	windows := []RunWindow{
		window("run-a", baseTime, baseTime.Add(minute(10))),
		window("run-b", baseTime.Add(minute(20)), baseTime.Add(minute(40))),
	}
	report := EquipmentReport(store, eq, baseTime, baseTime.Add(minute(40)), windows)

	runA := findRun(t, report.Runs, "run-a")
	runB := findRun(t, report.Runs, "run-b")
	if !approx(runA.Energy, 2) {
		t.Fatalf("run-a want 2 kWh (10/40 of 8), got %.4f", runA.Energy)
	}
	if !approx(runB.Energy, 4) {
		t.Fatalf("run-b want 4 kWh (20/40 of 8), got %.4f", runB.Energy)
	}
	if !approx(report.UnattributedKWh, 2) {
		t.Fatalf("idle 10 minutes want 2 kWh unattributed, got %.4f", report.UnattributedKWh)
	}
	if !approx(report.AllocatedKWh+report.UnattributedKWh, 8) {
		t.Fatalf("books do not reconcile: %.4f + %.4f != 8", report.AllocatedKWh, report.UnattributedKWh)
	}
}

// CT multiplier: 2 dial units through a x4 meter is 8 kWh.
func TestAllocateWithMultiplier(t *testing.T) {
	const eq = "station-a/compressor"
	store := newTestStore(t, testMeter("m1", eq, 4, baseTime, time.Time{}))
	addReading(t, store, "m1", eq, 100, baseTime)
	addReading(t, store, "m1", eq, 102, baseTime.Add(minute(40)))

	report := EquipmentReport(store, eq, baseTime, baseTime.Add(minute(40)),
		[]RunWindow{window("run-1", baseTime, baseTime.Add(minute(40)))})

	if !approx(report.AllocatedKWh, 8) {
		t.Fatalf("want 8 kWh after x4 multiplier, got %.4f", report.AllocatedKWh)
	}
}

// Mid-operation meter replacement with no time gap: both meters' segments are
// allocated to the run and no replacement anomaly is raised.
func TestMeterReplacementContiguous(t *testing.T) {
	const eq = "station-a/compressor"
	switchAt := baseTime.Add(minute(20))
	store := NewStore()
	if err := store.InstallMeter(testMeter("old", eq, 1, baseTime, switchAt)); err != nil {
		t.Fatal(err)
	}
	if err := store.InstallMeter(testMeter("new", eq, 1, switchAt, time.Time{})); err != nil {
		t.Fatal(err)
	}
	addReading(t, store, "old", eq, 0, baseTime)
	addReading(t, store, "old", eq, 5, switchAt)
	addReading(t, store, "new", eq, 0, switchAt)
	addReading(t, store, "new", eq, 3, baseTime.Add(minute(40)))

	report := EquipmentReport(store, eq, baseTime, baseTime.Add(minute(40)),
		[]RunWindow{window("run-1", baseTime, baseTime.Add(minute(40)))})

	if len(report.Segments) != 2 {
		t.Fatalf("want one segment per meter, got %d", len(report.Segments))
	}
	if !approx(report.AllocatedKWh, 8) || !approx(report.UnattributedKWh, 0) {
		t.Fatalf("want 8 allocated / 0 unattributed across replacement, got %.4f / %.4f", report.AllocatedKWh, report.UnattributedKWh)
	}
	if hasAnomaly(report.Anomalies, model.AnomalyReplaceGap) {
		t.Fatalf("contiguous replacement must not flag a gap: %+v", report.Anomalies)
	}
	run := findRun(t, report.Runs, "run-1")
	meters := map[string]bool{}
	for _, share := range run.Shares {
		meters[share.MeterID] = true
	}
	if !meters["old"] || !meters["new"] {
		t.Fatalf("run shares must cite both meters, got %+v", run.Shares)
	}
}

// A gap between retiring the old meter and installing the replacement is
// reported as an anomaly; measured energy before/after still allocates.
func TestMeterReplacementGap(t *testing.T) {
	const eq = "station-a/compressor"
	retiredAt := baseTime.Add(minute(20))
	installedAt := baseTime.Add(minute(30))
	store := NewStore()
	if err := store.InstallMeter(testMeter("old", eq, 1, baseTime, retiredAt)); err != nil {
		t.Fatal(err)
	}
	if err := store.InstallMeter(testMeter("new", eq, 1, installedAt, time.Time{})); err != nil {
		t.Fatal(err)
	}
	addReading(t, store, "old", eq, 0, baseTime)
	addReading(t, store, "old", eq, 5, retiredAt)
	addReading(t, store, "new", eq, 0, installedAt)
	addReading(t, store, "new", eq, 3, baseTime.Add(minute(40)))

	report := EquipmentReport(store, eq, baseTime, baseTime.Add(minute(40)),
		[]RunWindow{window("run-1", baseTime, baseTime.Add(minute(40)))})

	if !hasAnomaly(report.Anomalies, model.AnomalyReplaceGap) {
		t.Fatalf("want replacement_gap anomaly, got %+v", report.Anomalies)
	}
	if !approx(report.AllocatedKWh, 8) {
		t.Fatalf("measured energy still allocates: want 8 got %.4f", report.AllocatedKWh)
	}
}

// Installing a replacement before the old meter is retired is rejected.
func TestRejectOverlappingMeterInstall(t *testing.T) {
	store := NewStore()
	if err := store.InstallMeter(testMeter("old", "eq", 1, baseTime, time.Time{})); err != nil {
		t.Fatal(err)
	}
	err := store.InstallMeter(testMeter("new", "eq", 1, baseTime.Add(minute(10)), time.Time{}))
	if err == nil {
		t.Fatal("expected error installing meter while old one is active")
	}
}

// A dial rollback invalidates only that delta; the run keeps the trustworthy
// energy before and after, and a rollback anomaly records both readings.
func TestReadingRollbackExcluded(t *testing.T) {
	const eq = "station-a/compressor"
	store := newTestStore(t, testMeter("m1", eq, 1, baseTime, time.Time{}))
	addReading(t, store, "m1", eq, 100, baseTime)
	addReading(t, store, "m1", eq, 110, baseTime.Add(minute(20)))
	addReading(t, store, "m1", eq, 105, baseTime.Add(minute(40)))
	addReading(t, store, "m1", eq, 115, baseTime.Add(minute(60)))

	report := EquipmentReport(store, eq, baseTime, baseTime.Add(minute(60)),
		[]RunWindow{window("run-1", baseTime, baseTime.Add(minute(60)))})

	if !hasAnomaly(report.Anomalies, model.AnomalyRollback) {
		t.Fatalf("want rollback anomaly, got %+v", report.Anomalies)
	}
	for _, a := range report.Anomalies {
		if a.Kind == model.AnomalyRollback && (!approx(a.ReadingFrom, 110) || !approx(a.ReadingTo, 105)) {
			t.Fatalf("rollback anomaly must cite both dials: %+v", a)
		}
	}
	if !approx(report.AllocatedKWh, 20) {
		t.Fatalf("want 10+10 trustworthy kWh allocated, got %.4f", report.AllocatedKWh)
	}
	if !approx(report.UnattributedKWh, 0) {
		t.Fatalf("rollback delta must not leak into unattributed, got %.4f", report.UnattributedKWh)
	}
	for _, segment := range report.Segments {
		if !segment.Valid && segment.Energy != 0 {
			t.Fatalf("invalid segment must carry zero energy: %+v", segment)
		}
	}
}

// Time past the last known reading cannot be measured: coverage gap anomaly,
// measured energy before it still allocates.
func TestCoverageBeyondLastReading(t *testing.T) {
	const eq = "station-a/compressor"
	store := newTestStore(t, testMeter("m1", eq, 1, baseTime, time.Time{}))
	addReading(t, store, "m1", eq, 100, baseTime)
	addReading(t, store, "m1", eq, 110, baseTime.Add(minute(20)))

	report := EquipmentReport(store, eq, baseTime, baseTime.Add(minute(40)),
		[]RunWindow{window("run-1", baseTime, baseTime.Add(minute(40)))})

	if !hasAnomaly(report.Anomalies, model.AnomalyCoverageGap) {
		t.Fatalf("want coverage gap anomaly, got %+v", report.Anomalies)
	}
	if !approx(report.AllocatedKWh, 10) || !approx(report.UnattributedKWh, 0) {
		t.Fatalf("want 10 allocated / 0 unattributed, got %.4f / %.4f", report.AllocatedKWh, report.UnattributedKWh)
	}
}

// Two readings with the same timestamp cannot place their delta in time.
func TestDuplicateTimestampFlagged(t *testing.T) {
	const eq = "station-a/compressor"
	store := newTestStore(t, testMeter("m1", eq, 1, baseTime, time.Time{}))
	addReading(t, store, "m1", eq, 100, baseTime)
	addReading(t, store, "m1", eq, 105, baseTime)
	addReading(t, store, "m1", eq, 110, baseTime.Add(minute(20)))

	report := EquipmentReport(store, eq, baseTime, baseTime.Add(minute(20)),
		[]RunWindow{window("run-1", baseTime, baseTime.Add(minute(20)))})

	if !hasAnomaly(report.Anomalies, model.AnomalyDuplicateTime) {
		t.Fatalf("want duplicate timestamp anomaly, got %+v", report.Anomalies)
	}
	if !approx(report.AllocatedKWh, 5) {
		t.Fatalf("only the placeable 5 kWh should allocate, got %.4f", report.AllocatedKWh)
	}
}

// Overlapping run windows are resolved deterministically (earlier window wins)
// and total energy still reconciles.
func TestOverlappingWindowsReconcile(t *testing.T) {
	const eq = "station-a/compressor"
	store := newTestStore(t, testMeter("m1", eq, 1, baseTime, time.Time{}))
	addReading(t, store, "m1", eq, 100, baseTime)
	addReading(t, store, "m1", eq, 108, baseTime.Add(minute(40)))

	windows := []RunWindow{
		window("run-a", baseTime, baseTime.Add(minute(30))),
		window("run-b", baseTime.Add(minute(20)), baseTime.Add(minute(40))),
	}
	report := EquipmentReport(store, eq, baseTime, baseTime.Add(minute(40)), windows)

	runA := findRun(t, report.Runs, "run-a")
	runB := findRun(t, report.Runs, "run-b")
	if !approx(runA.Energy, 6) || !approx(runB.Energy, 2) {
		t.Fatalf("want 6 / 2 split, got %.4f / %.4f", runA.Energy, runB.Energy)
	}
	if !approx(report.AllocatedKWh+report.UnattributedKWh, 8) {
		t.Fatalf("books do not reconcile: %.4f + %.4f", report.AllocatedKWh, report.UnattributedKWh)
	}
}

// A segment crossing the report boundary is time-clipped, so reports for
// adjacent windows add up to the dial delta.
func TestSegmentClippedAtReportBoundary(t *testing.T) {
	const eq = "station-a/compressor"
	store := newTestStore(t, testMeter("m1", eq, 1, baseTime, time.Time{}))
	addReading(t, store, "m1", eq, 100, baseTime)
	addReading(t, store, "m1", eq, 108, baseTime.Add(minute(40)))

	first := EquipmentReport(store, eq, baseTime, baseTime.Add(minute(10)),
		[]RunWindow{window("run-1", baseTime, baseTime.Add(minute(40)))})
	second := EquipmentReport(store, eq, baseTime.Add(minute(10)), baseTime.Add(minute(40)),
		[]RunWindow{window("run-1", baseTime, baseTime.Add(minute(40)))})

	if !approx(first.AllocatedKWh, 2) || !approx(second.AllocatedKWh, 6) {
		t.Fatalf("clipped reports want 2 and 6 kWh, got %.4f and %.4f", first.AllocatedKWh, second.AllocatedKWh)
	}
}

// Out-of-order entry is normalised: readings allocate by observation time.
func TestReadingsEnteredOutOfOrder(t *testing.T) {
	const eq = "station-a/compressor"
	m := testMeter("m1", eq, 1, baseTime, time.Time{})
	store := newTestStore(t, m)
	addReading(t, store, "m1", eq, 108, baseTime.Add(minute(40)))
	addReading(t, store, "m1", eq, 100, baseTime)

	readings := store.Readings("m1")
	if !readings[0].At.Equal(baseTime) || !approx(readings[0].Reading, 100) {
		t.Fatalf("readings not sorted by time: %+v", readings)
	}
	report := EquipmentReport(store, eq, baseTime, baseTime.Add(minute(40)),
		[]RunWindow{window("run-1", baseTime, baseTime.Add(minute(40)))})
	if !approx(report.AllocatedKWh, 8) {
		t.Fatalf("want 8 kWh after out-of-order entry, got %.4f", report.AllocatedKWh)
	}
}

func TestStoreRejectsBadInput(t *testing.T) {
	store := NewStore()
	if err := store.InstallMeter(testMeter("m", "eq", 0, baseTime, time.Time{})); err == nil {
		t.Fatal("zero multiplier must be rejected")
	}
	if err := store.InstallMeter(testMeter("m", "eq", 1, baseTime, baseTime.Add(-time.Hour))); err == nil {
		t.Fatal("retirement before installation must be rejected")
	}
}
