package energy

import (
	"math"
	"time"

	"github.com/wyw14/solo-036/internal/model"
)

// equipmentSegments builds the auditable segment list for one equipment inside
// [from, to]. Segments crossing a report boundary are clipped to the boundary
// and their energy is scaled by the remaining time fraction, so every report
// reconciles against the dial differences it actually covers.
func equipmentSegments(store *Store, equipmentID string, from, to time.Time) ([]model.EnergySegment, []model.Anomaly) {
	from = from.UTC()
	to = to.UTC()
	meters := store.MetersForEquipment(equipmentID)
	segments := make([]model.EnergySegment, 0)
	anomalies := make([]model.Anomaly, 0)

	if len(meters) == 0 {
		if to.After(from) {
			anomalies = append(anomalies, model.Anomaly{
				Kind:        model.AnomalyReplaceGap,
				EquipmentID: equipmentID,
				From:        from,
				To:          to,
				Message:     "no meter registered for equipment during the whole interval",
			})
		}
		return segments, anomalies
	}

	// Un-metered time before the first meter.
	if first := meters[0]; from.Before(first.InstalledAt) {
		gapEnd := minTime(to, first.InstalledAt)
		if gapEnd.After(from) {
			anomalies = append(anomalies, gapAnomaly(equipmentID, "", from, gapEnd, "no meter installed yet"))
		}
	}

	var previous *model.Meter
	for i := range meters {
		meter := meters[i]
		if previous != nil && previous.RetiredAt.Before(meter.InstalledAt) {
			gapStart := maxTime(from, previous.RetiredAt)
			gapEnd := minTime(to, meter.InstalledAt)
			if gapEnd.After(gapStart) {
				anomalies = append(anomalies, gapAnomaly(equipmentID, "", gapStart, gapEnd,
					"meter "+previous.ID+" retired before replacement "+meter.ID+" was installed"))
			}
		}
		meterSegments, meterAnomalies := meterSegments(store, meter, from, to)
		segments = append(segments, meterSegments...)
		anomalies = append(anomalies, meterAnomalies...)
		previous = &meters[i]
	}

	return segments, anomalies
}

func meterSegments(store *Store, meter model.Meter, from, to time.Time) ([]model.EnergySegment, []model.Anomaly) {
	readings := store.Readings(meter.ID)
	segments := make([]model.EnergySegment, 0)
	anomalies := make([]model.Anomaly, 0)

	// Part of the reporting window this meter is responsible for.
	coverageStart := maxTime(from, meter.InstalledAt)
	coverageEnd := to
	if !meter.RetiredAt.IsZero() {
		coverageEnd = minTime(to, meter.RetiredAt)
	}
	if !coverageEnd.After(coverageStart) {
		return segments, anomalies
	}

	// Readings outside the meter's service window cannot be explained by it.
	for _, reading := range readings {
		if reading.At.Before(meter.InstalledAt) || (!meter.RetiredAt.IsZero() && reading.At.After(meter.RetiredAt)) {
			anomalies = append(anomalies, model.Anomaly{
				Kind:        model.AnomalyCoverageGap,
				EquipmentID: meter.EquipmentID,
				MeterID:     meter.ID,
				From:        reading.At,
				To:          reading.At,
				Message:     "reading falls outside the meter service window",
			})
		}
	}

	if len(readings) == 0 {
		anomalies = append(anomalies, model.Anomaly{
			Kind:        model.AnomalyCoverageGap,
			EquipmentID: meter.EquipmentID,
			MeterID:     meter.ID,
			From:        coverageStart,
			To:          coverageEnd,
			Message:     "meter has no dial readings",
		})
		return segments, anomalies
	}

	// Consumption before the first known reading is unknown.
	if readings[0].At.After(coverageStart) {
		anomalies = append(anomalies, model.Anomaly{
			Kind:        model.AnomalyCoverageGap,
			EquipmentID: meter.EquipmentID,
			MeterID:     meter.ID,
			From:        coverageStart,
			To:          minTime(coverageEnd, readings[0].At),
			Message:     "no dial reading at or before the interval start",
		})
	}

	for i := 1; i < len(readings); i++ {
		prev := readings[i-1]
		cur := readings[i]

		if cur.At.Equal(prev.At) {
			anomalies = append(anomalies, model.Anomaly{
				Kind:        model.AnomalyDuplicateTime,
				EquipmentID: meter.EquipmentID,
				MeterID:     meter.ID,
				From:        cur.At,
				To:          cur.At,
				ReadingFrom: prev.Reading,
				ReadingTo:   cur.Reading,
				Message:     "two readings share one timestamp; delta cannot be placed in time",
			})
			continue
		}
		if cur.At.Before(prev.At) {
			// Readings are timestamp-sorted, so this only happens when storage
			// invariants were bypassed (e.g. restore of corrupt data).
			continue
		}

		segmentStart := maxTime(prev.At, coverageStart)
		segmentEnd := minTime(cur.At, coverageEnd)
		if !segmentEnd.After(segmentStart) {
			continue
		}
		segment := model.EnergySegment{
			EquipmentID: meter.EquipmentID,
			MeterID:     meter.ID,
			From:        segmentStart,
			To:          segmentEnd,
			DialFrom:    prev.Reading,
			DialTo:      cur.Reading,
			Multiplier:  meter.Multiplier,
		}
		if cur.Reading+1e-9 < prev.Reading {
			segment.Valid = false
			anomalies = append(anomalies, model.Anomaly{
				Kind:        model.AnomalyRollback,
				EquipmentID: meter.EquipmentID,
				MeterID:     meter.ID,
				From:        segmentStart,
				To:          segmentEnd,
				ReadingFrom: prev.Reading,
				ReadingTo:   cur.Reading,
				Message:     "dial reading went backwards; delta is excluded pending verification",
			})
		} else {
			segment.Valid = true
			fullDuration := cur.At.Sub(prev.At)
			fullEnergy := (cur.Reading - prev.Reading) * meter.Multiplier
			segment.Energy = scale(fullEnergy, segmentEnd.Sub(segmentStart), fullDuration)
		}
		segments = append(segments, segment)
	}

	// Past the last known reading nothing can be measured.
	last := readings[len(readings)-1]
	if coverageEnd.After(last.At) {
		message := "no dial reading covers the end of the interval"
		if !meter.RetiredAt.IsZero() && last.At.Before(meter.RetiredAt) {
			message = "meter retired without a final dial reading"
		}
		gapStart := maxTime(coverageStart, last.At)
		if coverageEnd.After(gapStart) {
			anomalies = append(anomalies, model.Anomaly{
				Kind:        model.AnomalyCoverageGap,
				EquipmentID: meter.EquipmentID,
				MeterID:     meter.ID,
				From:        gapStart,
				To:          coverageEnd,
				Message:     message,
			})
		}
	}
	return segments, anomalies
}

func gapAnomaly(equipmentID, meterID string, from, to time.Time, message string) model.Anomaly {
	return model.Anomaly{
		Kind:        model.AnomalyReplaceGap,
		EquipmentID: equipmentID,
		MeterID:     meterID,
		From:        from,
		To:          to,
		Message:     message,
	}
}

func scale(fullEnergy float64, part, total time.Duration) float64 {
	if total <= 0 || part >= total {
		if part >= total {
			return round6(fullEnergy)
		}
		return 0
	}
	return round6(fullEnergy * float64(part) / float64(total))
}

func round6(v float64) float64 { return math.Round(v*1e6) / 1e6 }

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
