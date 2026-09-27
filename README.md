PigFlow is a local HTTP control service for long-distance pipeline pigging operations.

## Energy metering

Compressor energy is tracked from cumulative kWh dial readings entered per
equipment and time, then allocated to runs on a time-linear basis.

- `POST /api/meters` — register a meter (`meter_id`, `equipment_id`,
  `multiplier` for CT ratio, `initial_reading`, `installed_at`). The initial
  reading is seeded automatically so coverage starts at installation.
- `POST /api/meters/{meterID}/retire` — close a meter before a replacement.
- `POST /api/meter-readings` — enter a cumulative dial reading
  (`meter_id`, `reading`, `at` RFC3339); out-of-order entry is allowed.
- `GET /api/meter-readings?meter_id=...` — reading history.
- `GET /api/energy?from=...&to=...&equipment=...` — per-equipment statement:
  dial-derived segments, per-run shares with their time-overlap basis,
  `unattributed_kwh` (measured while no run was active), `unknown_kwh` and
  anomalies.
- `GET /api/operations/{runID}/energy` — one run's allocation with each
  segment share (meter, overlap fraction, kWh) for verification.

Reading rollbacks (dial went backwards), duplicate timestamps, meter
replacement gaps, retirement without a final reading and intervals past the
last reading are reported as anomalies; the affected energy is never charged
to a run. Allocated + unattributed reconcile to the valid dial deltas.
Meters and readings are included in snapshots and restored on recovery.
