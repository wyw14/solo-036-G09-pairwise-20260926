package energy

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/wyw14/solo-036/internal/model"
)

var base = time.Date(2026, 9, 27, 8, 0, 0, 0, time.UTC)

func reading(id, meter string, at time.Time, kwh float64, kind model.ReadingKind) model.MeterReading {
	return model.MeterReading{
		ID:       id,
		DeviceID: "station-a/compressor",
		MeterID:  meter,
		KWh:      kwh,
		Kind:     kind,
		At:       at,
	}
}

func TestAddRejectsInvalidReadings(t *testing.T) {
	store := NewStore()
	if err := store.Add(reading("r1", "m1", base, 100, model.ReadingNormal)); err != nil {
		t.Fatalf("baseline reading: %v", err)
	}
	cases := []model.MeterReading{
		{DeviceID: "station-a/compressor", MeterID: "m1", KWh: 110, Kind: "bogus", At: base.Add(time.Hour)},
		{DeviceID: "station-a/compressor", MeterID: "m1", KWh: -1, Kind: model.ReadingNormal, At: base.Add(time.Hour)},
		{DeviceID: "station-a/compressor", MeterID: "m1", KWh: 110, Kind: model.ReadingNormal, At: base},
		{DeviceID: "station-a/compressor", MeterID: "m1", KWh: 90, Kind: model.ReadingNormal, At: base.Add(time.Hour)},
		{DeviceID: "station-b/compressor", MeterID: "m1", KWh: 110, Kind: model.ReadingNormal, At: base.Add(time.Hour)},
		{DeviceID: "", MeterID: "m2", KWh: 1, Kind: model.ReadingNormal, At: base},
		{DeviceID: "station-a/compressor", MeterID: "", KWh: 1, Kind: model.ReadingNormal, At: base},
	}
	for i, tc := range cases {
		if err := store.Add(tc); err == nil {
			t.Errorf("case %d: expected error for %+v", i, tc)
		}
	}
	if got := store.Count(); got != 1 {
		t.Fatalf("rejected readings must not be stored, got %d", got)
	}
}

func TestAddRollbackRequiresExplicitBaseline(t *testing.T) {
	store := NewStore()
	mustAdd(t, store, reading("r1", "m1", base, 100, model.ReadingNormal))
	mustAdd(t, store, reading("r2", "m1", base.Add(time.Hour), 150, model.ReadingNormal))
	if err := store.Add(reading("r3", "m1", base.Add(2*time.Hour), 40, model.ReadingNormal)); err == nil {
		t.Fatal("regressive normal reading must be rejected")
	}
	mustAdd(t, store, reading("r3", "m1", base.Add(2*time.Hour), 40, model.ReadingReset))
	mustAdd(t, store, reading("r4", "m1", base.Add(3*time.Hour), 55, model.ReadingNormal))
	if err := store.Add(reading("r5", "m1", base.Add(4*time.Hour), 30, model.ReadingNormal)); err == nil {
		t.Fatal("regression after a reset must still be rejected")
	}
}

func TestAddAllowsOutOfOrderBackfill(t *testing.T) {
	store := NewStore()
	mustAdd(t, store, reading("r1", "m1", base, 100, model.ReadingNormal))
	mustAdd(t, store, reading("r3", "m1", base.Add(2*time.Hour), 130, model.ReadingNormal))
	mustAdd(t, store, reading("r2", "m1", base.Add(time.Hour), 110, model.ReadingNormal))
	if err := store.Add(reading("r4", "m1", base.Add(90*time.Minute), 135, model.ReadingNormal)); err == nil {
		t.Fatal("backfill that overtakes the next reading must be rejected")
	}
	readings := store.Readings("station-a/compressor")
	if len(readings) != 3 || readings[1].ID != "r2" {
		t.Fatalf("readings must be stored in time order, got %+v", readings)
	}
}

func mustAdd(t *testing.T, store *Store, r model.MeterReading) {
	t.Helper()
	if err := store.Add(r); err != nil {
		t.Fatalf("add %+v: %v", r, err)
	}
}

func almostEqual(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestAllocateAttributesByOverlap(t *testing.T) {
	readings := []model.MeterReading{
		reading("r1", "m1", base, 1000, model.ReadingNormal),
		reading("r2", "m1", base.Add(time.Hour), 1010, model.ReadingNormal),
		reading("r3", "m1", base.Add(2*time.Hour), 1030, model.ReadingNormal),
	}
	windows := []RunWindow{
		{RunID: "run-1", Start: base.Add(30 * time.Minute), End: base.Add(90 * time.Minute)},
	}
	report := Allocate("station-a/compressor", readings, windows, time.Time{}, base.Add(2*time.Hour))
	if !almostEqual(report.MeasuredKWh, 30) {
		t.Errorf("measured = %v, want 30", report.MeasuredKWh)
	}
	if len(report.Runs) != 1 {
		t.Fatalf("runs = %+v, want exactly run-1", report.Runs)
	}
	// run-1 covers half of each interval: 10*0.5 + 20*0.5 = 15
	if got := report.Runs[0].KWh; !almostEqual(got, 15) {
		t.Errorf("run-1 kwh = %v, want 15", got)
	}
	if !almostEqual(report.AllocatedKWh, 15) || !almostEqual(report.UnallocatedKWh, 15) {
		t.Errorf("allocated %v + unallocated %v must equal measured 30", report.AllocatedKWh, report.UnallocatedKWh)
	}
	if len(report.Runs[0].Basis) != 2 {
		t.Fatalf("basis = %+v, want one entry per interval", report.Runs[0].Basis)
	}
	basis := report.Runs[0].Basis[0]
	if basis.FromReadingID != "r1" || basis.ToReadingID != "r2" || !almostEqual(basis.IntervalKWh, 10) {
		t.Errorf("basis must reference the underlying readings, got %+v", basis)
	}
	if !almostEqual(basis.OverlapSeconds, 1800) || !almostEqual(basis.Share, 0.5) || !almostEqual(basis.KWh, 5) {
		t.Errorf("basis overlap/share/kwh = %v/%v/%v, want 1800/0.5/5", basis.OverlapSeconds, basis.Share, basis.KWh)
	}
	if len(report.Gaps) != 0 {
		t.Errorf("no gaps expected, got %+v", report.Gaps)
	}
}

func TestAllocateSplitsConcurrentRunsEvenly(t *testing.T) {
	readings := []model.MeterReading{
		reading("r1", "m1", base, 100, model.ReadingNormal),
		reading("r2", "m1", base.Add(time.Hour), 160, model.ReadingNormal),
	}
	windows := []RunWindow{
		{RunID: "run-1", Start: base, End: base.Add(time.Hour)},
		{RunID: "run-2", Start: base.Add(30 * time.Minute), End: base.Add(90 * time.Minute)},
	}
	report := Allocate("station-a/compressor", readings, windows, time.Time{}, base.Add(2*time.Hour))
	// first half hour: run-1 alone -> 30; second half hour: shared -> 15 each
	if got := report.Runs[0].KWh; !almostEqual(got, 45) {
		t.Errorf("run-1 kwh = %v, want 45", got)
	}
	if got := report.Runs[1].KWh; !almostEqual(got, 15) {
		t.Errorf("run-2 kwh = %v, want 15", got)
	}
	if !almostEqual(report.AllocatedKWh, 60) || !almostEqual(report.UnallocatedKWh, 0) {
		t.Errorf("allocated %v + unallocated %v must equal measured 60", report.AllocatedKWh, report.UnallocatedKWh)
	}
}

func TestAllocateReportsReplacementAndResetGaps(t *testing.T) {
	readings := []model.MeterReading{
		reading("r1", "m1", base, 100, model.ReadingNormal),
		reading("r2", "m1", base.Add(time.Hour), 150, model.ReadingNormal),
		reading("r3", "m1", base.Add(2*time.Hour), 20, model.ReadingReplacement),
		reading("r4", "m1", base.Add(3*time.Hour), 50, model.ReadingNormal),
	}
	report := Allocate("station-a/compressor", readings, nil, time.Time{}, base.Add(4*time.Hour))
	if !almostEqual(report.MeasuredKWh, 80) {
		t.Errorf("measured = %v, want 80 (50 before + 30 after the swap)", report.MeasuredKWh)
	}
	if !almostEqual(report.UnallocatedKWh, 80) {
		t.Errorf("unallocated = %v, want 80 with no runs", report.UnallocatedKWh)
	}
	if len(report.Gaps) != 1 {
		t.Fatalf("gaps = %+v, want the replacement interval", report.Gaps)
	}
	gap := report.Gaps[0]
	if gap.Reason != string(model.ReadingReplacement) || !gap.FromAt.Equal(base.Add(time.Hour)) || !gap.ToAt.Equal(base.Add(2*time.Hour)) {
		t.Errorf("gap = %+v, want replacement between r2 and r3", gap)
	}
}

func TestAllocateClipsToReportWindow(t *testing.T) {
	readings := []model.MeterReading{
		reading("r1", "m1", base, 1000, model.ReadingNormal),
		reading("r2", "m1", base.Add(2*time.Hour), 1040, model.ReadingNormal),
	}
	windows := []RunWindow{
		{RunID: "run-1", Start: base, End: base.Add(2 * time.Hour)},
	}
	from := base.Add(time.Hour)
	report := Allocate("station-a/compressor", readings, windows, from, base.Add(2*time.Hour))
	if !almostEqual(report.MeasuredKWh, 20) {
		t.Errorf("measured = %v, want 20 for the clipped half", report.MeasuredKWh)
	}
	if got := report.Runs[0].KWh; !almostEqual(got, 20) {
		t.Errorf("run-1 kwh = %v, want 20", got)
	}
	basis := report.Runs[0].Basis[0]
	if !almostEqual(basis.IntervalKWh, 40) || !almostEqual(basis.OverlapSeconds, 3600) || !almostEqual(basis.KWh, 20) {
		t.Errorf("basis = %+v, want full interval 40 kwh with 3600s overlap giving 20 kwh", basis)
	}
}

func TestRestoreReplaysJournaledReadings(t *testing.T) {
	store := NewStore()
	mustAdd(t, store, reading("r1", "m1", base, 100, model.ReadingNormal))
	mustAdd(t, store, reading("r2", "m1", base.Add(time.Hour), 160, model.ReadingNormal))
	events := make([]model.Event, 0)
	for _, recorded := range store.Readings("") {
		payload, err := json.Marshal(recorded)
		if err != nil {
			t.Fatalf("marshal reading: %v", err)
		}
		events = append(events, model.Event{Kind: MeterReadingEventKind, Payload: string(payload)})
	}
	events = append(events, model.Event{Kind: "launch", Payload: "ignored"})
	events = append(events, model.Event{Kind: MeterReadingEventKind, Payload: "{broken"})

	restored := NewStore()
	if got := Restore(restored, events); got != 2 {
		t.Fatalf("restored = %d, want 2", got)
	}
	if restored.Count() != 2 {
		t.Fatalf("restored store holds %d readings, want 2", restored.Count())
	}
}
