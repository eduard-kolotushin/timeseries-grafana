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

func (a *App) handlePing(w http.ResponseWriter, _ *http.Request) {
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
	if err := checkTrainLen(len(body.Times), len(body.Values)); err != nil {
		http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
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
}
