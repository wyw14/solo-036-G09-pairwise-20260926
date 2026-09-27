package energy

import (
	"sort"
	"time"

	"github.com/wyw14/solo-036/internal/model"
)

// RunWindow is a run's energy-relevant interval on one piece of equipment.
// End is zero for runs that are still active; their window is open.
type RunWindow struct {
	RunID     string
	StationID string
	State     model.RunState
	Start     time.Time
	End       time.Time
}

type coveredPart struct {
	window RunWindow
	from   time.Time
	to     time.Time
}

// Allocate splits valid segment energy across run windows on a linear-time
// basis: a segment's kWh is divided in proportion to how much of its time
// range each run window covers. Energy measured while no window covers the
// equipment is returned as unattributed (idle / between jobs). Invalid
// segments (rollback, duplicate timestamps) never enter allocation.
//
// Windows are expected not to overlap (one station runs one job at a time).
// If stored data does overlap, the window that started first keeps the
// overlap so the energy total is conserved.
func Allocate(segments []model.EnergySegment, windows []RunWindow) ([]model.RunEnergy, float64) {
	clipped := clipWindows(windows)
	totals := make(map[string]*model.RunEnergy)
	order := make([]string, 0)
	var unattributed float64

	for _, segment := range segments {
		if !segment.Valid || segment.Energy <= 0 {
			continue
		}
		parts := coveringParts(segment, clipped)
		segmentDuration := segment.To.Sub(segment.From)
		allocated := 0.0
		for _, part := range parts {
			overlap := part.to.Sub(part.from)
			fraction := float64(overlap) / float64(segmentDuration)
			shareEnergy := round6(segment.Energy * fraction)
			if shareEnergy <= 0 {
				continue
			}
			result, exists := totals[part.window.RunID]
			if !exists {
				result = &model.RunEnergy{
					RunID:     part.window.RunID,
					StationID: part.window.StationID,
					State:     part.window.State,
					From:      part.window.Start,
					To:        part.window.End,
					Shares:    make([]model.EnergyShare, 0),
				}
				totals[part.window.RunID] = result
				order = append(order, part.window.RunID)
			}
			result.Shares = append(result.Shares, model.EnergyShare{
				RunID:           part.window.RunID,
				SegmentFrom:     segment.From,
				SegmentTo:       segment.To,
				MeterID:         segment.MeterID,
				From:            part.from,
				To:              part.to,
				Energy:          shareEnergy,
				OverlapDuration: overlap,
				SegmentDuration: segmentDuration,
				Fraction:        fraction,
			})
			allocated += shareEnergy
		}
		// Residual after rounding stays with the equipment, never silently
		// lost: allocated shares + unattributed exactly reconcile to the
		// segment energy.
		unattributed += round6(segment.Energy - allocated)
	}

	runs := make([]model.RunEnergy, 0, len(order))
	for _, id := range order {
		result := totals[id]
		result.Energy = round6(sumShares(result.Shares))
		sort.Slice(result.Shares, func(i, j int) bool {
			if result.Shares[i].From.Equal(result.Shares[j].From) {
				return result.Shares[i].MeterID < result.Shares[j].MeterID
			}
			return result.Shares[i].From.Before(result.Shares[j].From)
		})
		runs = append(runs, *result)
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].From.Before(runs[j].From) })
	return runs, round6(unattributed)
}

// clipWindows returns windows sorted by start with overlaps removed: when two
// windows overlap the earlier window retains the shared time range.
func clipWindows(windows []RunWindow) []RunWindow {
	sorted := append([]RunWindow(nil), windows...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Start.Equal(sorted[j].Start) {
			return sorted[i].RunID < sorted[j].RunID
		}
		return sorted[i].Start.Before(sorted[j].Start)
	})
	clipped := make([]RunWindow, 0, len(sorted))
	for _, w := range sorted {
		if len(clipped) > 0 {
			previous := &clipped[len(clipped)-1]
			if previous.End.IsZero() {
				continue // open-ended earlier window absorbs everything after it
			}
			if w.Start.Before(previous.End) && (w.End.IsZero() || w.End.After(previous.End)) {
				w.Start = previous.End
			} else if !w.End.IsZero() && !w.End.After(previous.End) {
				continue // fully contained in the earlier window
			}
		}
		clipped = append(clipped, w)
	}
	return clipped
}

func coveringParts(segment model.EnergySegment, windows []RunWindow) []coveredPart {
	parts := make([]coveredPart, 0)
	for _, w := range windows {
		start := maxTime(segment.From, w.Start)
		end := segment.To
		if !w.End.IsZero() {
			end = minTime(segment.To, w.End)
		}
		if end.After(start) {
			parts = append(parts, coveredPart{window: w, from: start, to: end})
		}
	}
	return parts
}

func sumShares(shares []model.EnergyShare) float64 {
	total := 0.0
	for _, share := range shares {
		total += share.Energy
	}
	return total
}
