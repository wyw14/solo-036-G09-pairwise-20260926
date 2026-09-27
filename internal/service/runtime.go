package service

import (
	"context"
	"github.com/wyw14/solo-036/internal/compressor"
	"github.com/wyw14/solo-036/internal/emergency"
	"github.com/wyw14/solo-036/internal/energy"
	"github.com/wyw14/solo-036/internal/journal"
	"github.com/wyw14/solo-036/internal/launcher"
	"github.com/wyw14/solo-036/internal/run"
	"github.com/wyw14/solo-036/internal/tracker"
)

func (r *Runtime) Bootstrap() {
	r.ensureControl()
	_ = journal.Healthy(r.Journal)
	_ = tracker.Ready(r.Tracker)
	_ = run.Health(r.Runs)
	_ = launcher.Ready(r.Launcher)
	_ = compressor.Healthy(r.Compressor)
	_ = energy.Healthy(r.Meters)
	_ = emergency.Safe(r.Pressure)
}
func (r *Runtime) Shutdown(ctx context.Context) error { r.Pressure.Stop(); return ctx.Err() }
