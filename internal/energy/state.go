package energy

import (
	"github.com/wyw14/solo-036/internal/model"
	"sync"
)

// Store is the thread-safe registry of meters and their cumulative dial
// readings. Readings are kept per meter in timestamp order.
type Store struct {
	mu       sync.RWMutex
	meters   map[string]model.Meter
	readings map[string][]model.MeterReading
}

func NewStore() *Store {
	return &Store{
		meters:   make(map[string]model.Meter),
		readings: make(map[string][]model.MeterReading),
	}
}
