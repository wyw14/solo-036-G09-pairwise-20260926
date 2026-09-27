package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/wyw14/solo-036/internal/model"
	"github.com/wyw14/solo-036/internal/service"
)

func (h *Handler) RegisterMeter(w http.ResponseWriter, r *http.Request) {
	var request service.InstallMeterRequest
	if err := decodeBody(w, r, &request); err != nil {
		writeError(w, r, http.StatusBadRequest, err)
		return
	}
	meter, err := h.runtime.InstallMeter(request)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, meter)
}

func (h *Handler) ListMeters(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.runtime.ListMeters())
}

func (h *Handler) RetireMeter(w http.ResponseWriter, r *http.Request) {
	var request service.RetireMeterRequest
	if r.ContentLength != 0 {
		if err := decodeBody(w, r, &request); err != nil {
			writeError(w, r, http.StatusBadRequest, err)
			return
		}
	}
	meter, err := h.runtime.RetireMeter(chi.URLParam(r, "meterID"), request)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, meter)
}

func (h *Handler) AddMeterReading(w http.ResponseWriter, r *http.Request) {
	var request service.MeterReadingRequest
	if err := decodeBody(w, r, &request); err != nil {
		writeError(w, r, http.StatusBadRequest, err)
		return
	}
	if request.MeterID == "" {
		request.MeterID = chi.URLParam(r, "meterID")
	}
	reading, err := h.runtime.AddMeterReading(request)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, reading)
}

func (h *Handler) ListMeterReadings(w http.ResponseWriter, r *http.Request) {
	readings := h.runtime.ListMeterReadings()
	meterID := strings.TrimSpace(r.URL.Query().Get("meter_id"))
	if meterID == "" {
		writeJSON(w, http.StatusOK, readings)
		return
	}
	filtered := make([]model.MeterReading, 0)
	for _, reading := range readings {
		if reading.MeterID == meterID {
			filtered = append(filtered, reading)
		}
	}
	writeJSON(w, http.StatusOK, filtered)
}

func (h *Handler) EnergyReport(w http.ResponseWriter, r *http.Request) {
	from, to, err := parseEnergyRange(r)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, err)
		return
	}
	equipment := r.URL.Query()["equipment"]
	writeJSON(w, http.StatusOK, h.runtime.EnergyReport(from, to, equipment))
}

func (h *Handler) RunEnergy(w http.ResponseWriter, r *http.Request) {
	from, to, err := parseEnergyRange(r)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, err)
		return
	}
	report, err := h.runtime.RunEnergy(chi.URLParam(r, "runID"), from, to)
	if err != nil {
		writeError(w, r, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func parseEnergyRange(r *http.Request) (time.Time, time.Time, error) {
	var from, to time.Time
	if raw := strings.TrimSpace(r.URL.Query().Get("from")); raw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return time.Time{}, time.Time{}, errors.New("from must use RFC3339 format")
		}
		from = parsed.UTC()
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("to")); raw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return time.Time{}, time.Time{}, errors.New("to must use RFC3339 format")
		}
		to = parsed.UTC()
	}
	if !to.IsZero() && !from.IsZero() && !to.After(from) {
		return time.Time{}, time.Time{}, errors.New("to must be after from")
	}
	return from, to, nil
}
