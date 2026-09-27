package energy

import (
	"sort"
	"time"

	"github.com/wyw14/solo-036/internal/model"
)

// EquipmentReport builds the auditable energy statement for one piece of
// equipment: dial-derived segments, per-run allocation, measured but
// unattributed energy and every anomaly encountered.
func EquipmentReport(store *Store, equipmentID string, from, to time.Time, windows []RunWindow) model.EquipmentEnergy {
	from = from.UTC()
	to = to.UTC()
	segments, anomalies := equipmentSegments(store, equipmentID, from, to)
	runs, unattributed := Allocate(segments, windows)

	var allocated, unknown float64
	for _, segment := range segments {
		if segment.Valid {
			allocated += segment.Energy
		} else {
			unknown += segment.Energy
		}
	}
	// Allocate already returns valid energy not covered by a run window;
	// guard against floating drift between the two summation paths.
	allocated = round6(allocated)
	unattributed = round6(unattributed)

	sort.Slice(anomalies, func(i, j int) bool {
		if anomalies[i].From.Equal(anomalies[j].From) {
			return anomalies[i].Kind < anomalies[j].Kind
		}
		return anomalies[i].From.Before(anomalies[j].From)
	})
	return model.EquipmentEnergy{
		EquipmentID:     equipmentID,
		From:            from,
		To:              to,
		Segments:        segments,
		Runs:            runs,
		UnattributedKWh: unattributed,
		UnknownKWh:      round6(unknown),
		AllocatedKWh:    round6(allocated - unattributed),
		Anomalies:       anomalies,
	}
}

// Report builds statements for several pieces of equipment. Equipment without
// meters, runs or anomalies is omitted.
func Report(store *Store, equipmentIDs []string, windows map[string][]RunWindow, from, to time.Time) model.EnergyReport {
	seen := make(map[string]bool, len(equipmentIDs))
	ids := make([]string, 0, len(equipmentIDs))
	for _, id := range equipmentIDs {
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	out := model.EnergyReport{
		From:        from.UTC(),
		To:          to.UTC(),
		GeneratedAt: time.Now().UTC(),
		Equipment:   make([]model.EquipmentEnergy, 0, len(ids)),
	}
	for _, id := range ids {
		statement := EquipmentReport(store, id, from, to, windows[id])
		if len(statement.Segments) == 0 && len(statement.Anomalies) == 0 && len(statement.Runs) == 0 {
			continue
		}
		out.Equipment = append(out.Equipment, statement)
	}
	return out
}

func Healthy(store *Store) bool { return store != nil && store.meters != nil && store.readings != nil }
