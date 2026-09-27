package energy

import (
	"encoding/json"

	"github.com/wyw14/solo-036/internal/model"
)

const MeterReadingEventKind = "meter_reading"

// Restore replays journaled meter reading events back into the store.
// Readings were validated before being journaled, so replay rarely fails;
// unreadable or invalid entries are skipped to keep recovery total.
func Restore(store *Store, events []model.Event) int {
	recovered := 0
	for _, event := range events {
		if event.Kind != MeterReadingEventKind {
			continue
		}
		var reading model.MeterReading
		if err := json.Unmarshal([]byte(event.Payload), &reading); err != nil {
			continue
		}
		if err := store.Add(reading); err != nil {
			continue
		}
		recovered++
	}
	return recovered
}
