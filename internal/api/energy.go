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

type readingRequest struct {
	DeviceID string  `json:"device_id"`
	MeterID  string  `json:"meter_id"`
	KWh      float64 `json:"kwh"`
	Kind     string  `json:"kind"`
	Note     string  `json:"note"`
	At       string  `json:"at"`
}

func parseOptionalInstant(value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, errors.New("timestamp must use RFC3339 format")
	}
	return parsed.UTC(), nil
}

func (h *Handler) RecordReading(w http.ResponseWriter, r *http.Request) {
	var request readingRequest
	if err := decodeBody(w, r, &request); err != nil {
		writeError(w, r, http.StatusBadRequest, err)
		return
	}
	at, err := parseOptionalInstant(request.At)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, err)
		return
	}
	reading, err := h.runtime.RecordReading(service.ReadingRequest{
		DeviceID: request.DeviceID,
		MeterID:  request.MeterID,
		KWh:      request.KWh,
		Kind:     model.ReadingKind(request.Kind),
		Note:     request.Note,
		At:       at,
	})
	if err != nil {
		writeError(w, r, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusCreated, reading)
}

func (h *Handler) MeterReadings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.runtime.MeterReadings(r.URL.Query().Get("device_id")))
}

func (h *Handler) EnergyAllocation(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	from, err := parseOptionalInstant(query.Get("from"))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, err)
		return
	}
	to, err := parseOptionalInstant(query.Get("to"))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, err)
		return
	}
	report, err := h.runtime.EnergyAllocation(query.Get("device_id"), from, to)
	if err != nil {
		writeError(w, r, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (h *Handler) RunEnergy(w http.ResponseWriter, r *http.Request) {
	allocation, err := h.runtime.RunEnergy(chi.URLParam(r, "runID"))
	if err != nil {
		writeError(w, r, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, allocation)
}
