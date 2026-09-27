package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wyw14/solo-036/internal/model"
	"github.com/wyw14/solo-036/internal/service"
)

func newEnergyRouter() (http.Handler, *service.Runtime) {
	rt := service.NewRuntime()
	rt.Bootstrap()
	return NewRouter(rt), rt
}

func doJSON(t *testing.T, router http.Handler, method, target, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("content-type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var decoded map[string]any
	if rec.Code < 400 && rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("decode response %q: %v", rec.Body.String(), err)
		}
	}
	return rec, decoded
}

func TestMeterAndReadingEndToEnd(t *testing.T) {
	router, _ := newEnergyRouter()
	from := time.Date(2026, 9, 12, 8, 0, 0, 0, time.UTC)
	to := from.Add(40 * time.Minute)

	rec, _ := doJSON(t, router, http.MethodPost, "/api/meters",
		`{"meter_id":"m-1","equipment_id":"station-a/compressor","multiplier":1,"installed_at":"`+from.Format(time.RFC3339)+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register meter: %d %s", rec.Code, rec.Body.String())
	}

	rec, _ = doJSON(t, router, http.MethodPost, "/api/meter-readings",
		`{"meter_id":"m-1","reading":12,"at":"`+to.Format(time.RFC3339)+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add reading: %d %s", rec.Code, rec.Body.String())
	}

	rec, body := doJSON(t, router, http.MethodGet,
		"/api/energy?from="+from.Format(time.RFC3339)+"&to="+to.Format(time.RFC3339), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("energy report: %d %s", rec.Code, rec.Body.String())
	}
	equipment, ok := body["equipment"].([]any)
	if !ok || len(equipment) != 1 {
		t.Fatalf("want one equipment statement, got %v", body["equipment"])
	}
	statement := equipment[0].(map[string]any)
	if statement["unattributed_kwh"].(float64) != 12 {
		t.Fatalf("meter-only energy with no run must be unattributed: %v", statement)
	}

	// Readings list with meter filter.
	req := httptest.NewRequest(http.MethodGet, "/api/meter-readings?meter_id=m-1", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list readings: %d", rec.Code)
	}
	var readings []model.MeterReading
	if err := json.Unmarshal(rec.Body.Bytes(), &readings); err != nil {
		t.Fatal(err)
	}
	if len(readings) != 2 { // initial + manual
		t.Fatalf("want 2 readings, got %d", len(readings))
	}
}

func TestEnergyReportRejectsBadRange(t *testing.T) {
	router, _ := newEnergyRouter()
	rec, _ := doJSON(t, router, http.MethodGet,
		"/api/energy?from=2026-09-12T09:00:00Z&to=2026-09-12T08:00:00Z", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("inverted range must be 400, got %d", rec.Code)
	}
}

func TestRetireMeterThenReadingRejected(t *testing.T) {
	router, _ := newEnergyRouter()
	from := time.Date(2026, 9, 12, 8, 0, 0, 0, time.UTC)
	doJSON(t, router, http.MethodPost, "/api/meters",
		`{"meter_id":"m-1","equipment_id":"station-a/compressor","installed_at":"`+from.Format(time.RFC3339)+`"}`)
	rec, _ := doJSON(t, router, http.MethodPost, "/api/meters/m-1/retire",
		`{"retired_at":"`+from.Add(time.Hour).Format(time.RFC3339)+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("retire: %d %s", rec.Code, rec.Body.String())
	}
	rec, _ = doJSON(t, router, http.MethodPost, "/api/meter-readings",
		`{"meter_id":"m-1","reading":3,"at":"`+from.Add(2*time.Hour).Format(time.RFC3339)+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("post-retirement reading must be 400, got %d", rec.Code)
	}
}
