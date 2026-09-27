package energy

import (
	"sort"
	"time"

	"github.com/wyw14/solo-036/internal/model"
)

// RunWindow is the activity window of one job on the metered device.
type RunWindow struct {
	RunID string
	Start time.Time
	End   time.Time
}

// meterInterval is the measured consumption between two consecutive readings
// of one meter. start/end/kwh always describe the raw readings; effStart/
// effEnd/effKWh describe the portion inside the report window, assuming a
// uniform consumption rate across the interval.
type meterInterval struct {
	meterID       string
	fromReadingID string
	toReadingID   string
	start         time.Time
	end           time.Time
	kwh           float64
	effStart      time.Time
	effEnd        time.Time
	effKWh        float64
}

func intervals(readings []model.MeterReading) ([]meterInterval, []model.MeterGap) {
	measured := make([]meterInterval, 0, len(readings))
	gaps := make([]model.MeterGap, 0)
	for i := 1; i < len(readings); i++ {
		previous, current := readings[i-1], readings[i]
		if current.Kind.StartsBaseline() {
			gaps = append(gaps, model.MeterGap{
				MeterID: current.MeterID,
				FromAt:  previous.At,
				ToAt:    current.At,
				Reason:  string(current.Kind),
			})
			continue
		}
		measured = append(measured, meterInterval{
			meterID:       current.MeterID,
			fromReadingID: previous.ID,
			toReadingID:   current.ID,
			start:         previous.At,
			end:           current.At,
			kwh:           current.KWh - previous.KWh,
		})
	}
	return measured, gaps
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// Allocate attributes the measured consumption of one device to run windows.
// Every measured interval is split at run window boundaries into atomic
// pieces; a piece goes to the runs covering it (split evenly when concurrent)
// or to the unallocated share when no run is active. Intervals closed by a
// replacement or reset reading cannot be measured and are reported as gaps.
// A zero from is unbounded; a zero to means now.
func Allocate(deviceID string, readings []model.MeterReading, windows []RunWindow, from, to time.Time) model.EnergyReport {
	if to.IsZero() {
		to = time.Now().UTC()
	}
	report := model.EnergyReport{
		DeviceID: deviceID,
		From:     from,
		To:       to,
		Gaps:     make([]model.MeterGap, 0),
		Runs:     make([]model.RunAllocation, 0),
	}
	active := make([]RunWindow, 0, len(windows))
	for _, window := range windows {
		if window.RunID == "" || !window.End.After(window.Start) {
			continue
		}
		if !window.End.After(from) || !window.Start.Before(to) {
			continue
		}
		active = append(active, window)
	}
	sort.Slice(active, func(i, j int) bool { return active[i].RunID < active[j].RunID })

	byMeter := make(map[string][]model.MeterReading)
	for _, reading := range readings {
		byMeter[reading.MeterID] = append(byMeter[reading.MeterID], reading)
	}
	meterIDs := make([]string, 0, len(byMeter))
	for meterID := range byMeter {
		meterIDs = append(meterIDs, meterID)
	}
	sort.Strings(meterIDs)

	clipped := make([]meterInterval, 0)
	for _, meterID := range meterIDs {
		ordered := byMeter[meterID]
		sort.Slice(ordered, func(i, j int) bool { return ordered[i].At.Before(ordered[j].At) })
		measured, gaps := intervals(ordered)
		for _, gap := range gaps {
			if gap.ToAt.After(from) && gap.FromAt.Before(to) {
				report.Gaps = append(report.Gaps, gap)
			}
		}
		for _, interval := range measured {
			effStart := maxTime(interval.start, from)
			effEnd := minTime(interval.end, to)
			full := interval.end.Sub(interval.start).Seconds()
			if full <= 0 || !effEnd.After(effStart) {
				continue
			}
			interval.effStart = effStart
			interval.effEnd = effEnd
			interval.effKWh = interval.kwh * effEnd.Sub(effStart).Seconds() / full
			clipped = append(clipped, interval)
		}
	}
	sort.Slice(clipped, func(i, j int) bool {
		if clipped[i].effStart.Equal(clipped[j].effStart) {
			return clipped[i].meterID < clipped[j].meterID
		}
		return clipped[i].effStart.Before(clipped[j].effStart)
	})
	sort.Slice(report.Gaps, func(i, j int) bool {
		if report.Gaps[i].FromAt.Equal(report.Gaps[j].FromAt) {
			return report.Gaps[i].MeterID < report.Gaps[j].MeterID
		}
		return report.Gaps[i].FromAt.Before(report.Gaps[j].FromAt)
	})

	type runAccum struct {
		kwh   float64
		basis map[int]*model.AllocationBasis
	}
	accum := make(map[string]*runAccum)
	for _, window := range active {
		accum[window.RunID] = &runAccum{basis: make(map[int]*model.AllocationBasis)}
	}

	for index, interval := range clipped {
		report.MeasuredKWh += interval.effKWh
		bounds := []time.Time{interval.effStart, interval.effEnd}
		for _, window := range active {
			if window.End.After(interval.effStart) && window.Start.Before(interval.effEnd) {
				bounds = append(bounds, maxTime(window.Start, interval.effStart), minTime(window.End, interval.effEnd))
			}
		}
		sort.Slice(bounds, func(i, j int) bool { return bounds[i].Before(bounds[j]) })
		effDuration := interval.effEnd.Sub(interval.effStart).Seconds()
		for i := 1; i < len(bounds); i++ {
			pieceStart, pieceEnd := bounds[i-1], bounds[i]
			duration := pieceEnd.Sub(pieceStart).Seconds()
			if duration <= 0 {
				continue
			}
			kwh := interval.effKWh * duration / effDuration
			covering := make([]string, 0, len(active))
			for _, window := range active {
				if !window.Start.After(pieceStart) && !window.End.Before(pieceEnd) {
					covering = append(covering, window.RunID)
				}
			}
			if len(covering) == 0 {
				report.UnallocatedKWh += kwh
				continue
			}
			share := kwh / float64(len(covering))
			for _, runID := range covering {
				entry := accum[runID]
				entry.kwh += share
				basis, ok := entry.basis[index]
				if !ok {
					basis = &model.AllocationBasis{
						MeterID:       interval.meterID,
						FromReadingID: interval.fromReadingID,
						ToReadingID:   interval.toReadingID,
						IntervalStart: interval.start,
						IntervalEnd:   interval.end,
						IntervalKWh:   interval.kwh,
					}
					entry.basis[index] = basis
				}
				basis.OverlapSeconds += duration
				basis.KWh += share
			}
		}
	}

	for _, window := range active {
		entry := accum[window.RunID]
		allocation := model.RunAllocation{
			RunID:       window.RunID,
			WindowStart: window.Start,
			WindowEnd:   window.End,
			KWh:         entry.kwh,
			Basis:       make([]model.AllocationBasis, 0, len(entry.basis)),
		}
		indexes := make([]int, 0, len(entry.basis))
		for index := range entry.basis {
			indexes = append(indexes, index)
		}
		sort.Ints(indexes)
		for _, index := range indexes {
			basis := entry.basis[index]
			if clipped[index].effKWh > 0 {
				basis.Share = basis.KWh / clipped[index].effKWh
			}
			allocation.Basis = append(allocation.Basis, *basis)
		}
		report.AllocatedKWh += allocation.KWh
		report.Runs = append(report.Runs, allocation)
	}
	return report
}
