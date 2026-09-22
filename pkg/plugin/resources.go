package plugin

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

func recoverHTTP(w http.ResponseWriter) {
	if rec := recover(); rec != nil {
		http.Error(w, "forecast: panic", http.StatusInternalServerError)
	}
}

// handlePing answers the app resource route's health probe. It is GET-only, as
// docs/ARCHITECTURE.md documents it; nothing in the plugin or the datasource
// sends it any other method.
func (a *App) handlePing(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"message":"ok"}`))
}

func (a *App) handleForecast(w http.ResponseWriter, req *http.Request) {
	defer recoverHTTP(w)
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit := a.bodyLimit()
	if req.ContentLength > limit {
		http.Error(w, errBodyTooLarge.Error(), http.StatusRequestEntityTooLarge)
		return
	}
	// Refuse an impossible body before decoding it. Decoding is the expensive step:
	// it expands every body byte into an 8-byte slice element plus growth copies, so
	// a 16 MiB body would become >100 MB of live heap before checkTrainLen could
	// reject it — multiplied by every concurrent caller.
	if req.ContentLength > maxTrainBodyBytes() {
		http.Error(w, errTrainBodyTooLarge.Error(), http.StatusRequestEntityTooLarge)
		return
	}
	req.Body = http.MaxBytesReader(w, req.Body, limit)
	var body ForecastRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			http.Error(w, errBodyTooLarge.Error(), http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	orgID := backend.PluginConfigFromContext(req.Context()).OrgID
	out, err := a.dispatchForecast(req.Context(), orgID, body)
	if err != nil {
		http.Error(w, err.Error(), httpStatusFor(err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(out); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (a *App) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/ping", a.handlePing)
	mux.HandleFunc("/forecast", a.handleForecast)
	mux.HandleFunc("/schedules", a.handleSchedules)
	mux.HandleFunc("/schedules/default", a.handleScheduleDefault)
}
