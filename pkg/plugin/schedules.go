package plugin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

var (
	errAdminRequired    = errors.New("forecast: admin required")
	errNoStore          = errors.New("forecast: snapshot store not configured")
	errScopeKeyRequired = errors.New("forecast: scope and key required")
	errInvalidScope     = errors.New("forecast: invalid scope")
	errBaselineUnknown  = errors.New("forecast: no such baseline schedule; the baselines worker creates those rows, so an admin may only edit an existing one")
)

// maxScheduleBodyBytes caps the schedule routes' bodies. Their DTOs are a few
// hundred bytes; the cap exists only so a hostile caller cannot stream.
const maxScheduleBodyBytes = 64 << 10

// scheduleDTO is the /schedules wire shape. The stored spec is deliberately
// absent: it holds datasource query objects the UI has no use for, and echoing
// it would put query text into every Configuration page load.
type scheduleDTO struct {
	Scope      string `json:"scope"`
	Key        string `json:"key"`
	Cron       string `json:"cron"`
	Timezone   string `json:"timezone"`
	Enabled    bool   `json:"enabled"`
	NextRunAt  string `json:"nextRunAt,omitempty"`
	LastRunAt  string `json:"lastRunAt,omitempty"`
	LastStatus string `json:"lastStatus,omitempty"`
	HasSpec    bool   `json:"hasSpec"`
	// Source is derived from the stored spec; it is absent for a row without one
	// (a worker baseline row) and never carries the spec's query objects.
	Source *scheduleSourceDTO `json:"source,omitempty"`
}

// scheduleSourceDTO identifies what a row belongs to. The verbatim spec stays in
// the backend: this is the handful of fields the Retrain schedules table needs to
// say which dashboard panel trained a cache hash, and on what.
type scheduleSourceDTO struct {
	DashboardUID  string `json:"dashboardUid,omitempty"`
	PanelID       int    `json:"panelId,omitempty"`
	PanelTitle    string `json:"panelTitle,omitempty"`
	DatasourceUID string `json:"datasourceUid,omitempty"`
	SeriesName    string `json:"seriesName,omitempty"`
	QuerySummary  string `json:"querySummary,omitempty"`
	Lookback      string `json:"lookback,omitempty"`
}

// maxQuerySummary protects the list response from whatever a client stored: the
// summary labels a table cell, it is not a place to park a query.
const maxQuerySummary = 200

// scheduleSourceFromSpec reads only the identification keys of a stored spec.
// Unlike parseRetrainSpec it must never fail a listing: a malformed or absent spec
// (a baseline row has NULL) costs that row its Source cell, not the whole table.
func scheduleSourceFromSpec(raw []byte) *scheduleSourceDTO {
	if len(raw) == 0 {
		return nil
	}
	var src scheduleSourceDTO
	if err := json.Unmarshal(raw, &src); err != nil {
		return nil
	}
	if src == (scheduleSourceDTO{}) {
		return nil
	}
	if r := []rune(src.QuerySummary); len(r) > maxQuerySummary {
		src.QuerySummary = string(r[:maxQuerySummary-1]) + "…"
	}
	return &src
}

func toScheduleDTO(row ScheduleRow) scheduleDTO {
	out := scheduleDTO{
		Scope:      row.Scope,
		Key:        row.Key,
		Cron:       row.Cron,
		Timezone:   row.Timezone,
		Enabled:    row.Enabled,
		LastStatus: row.LastStatus,
		HasSpec:    len(row.Spec) > 0,
		Source:     scheduleSourceFromSpec(row.Spec),
	}
	if !row.NextRunAt.IsZero() {
		out.NextRunAt = row.NextRunAt.UTC().Format(time.RFC3339)
	}
	if !row.LastRunAt.IsZero() {
		out.LastRunAt = row.LastRunAt.UTC().Format(time.RFC3339)
	}
	return out
}

// adminOnly gates every schedule route: a schedule decides which panels retrain
// and when, so it is admin work rather than something any Viewer can rewrite.
func (a *App) adminOnly(w http.ResponseWriter, req *http.Request) bool {
	user := backend.PluginConfigFromContext(req.Context()).User
	if user == nil || user.Role != "Admin" {
		http.Error(w, errAdminRequired.Error(), http.StatusForbidden)
		return false
	}
	return true
}

// decodeBody mirrors handleForecast's cap-and-decode shape.
func decodeBody(w http.ResponseWriter, req *http.Request, into any) bool {
	if req.ContentLength > maxScheduleBodyBytes {
		http.Error(w, errBodyTooLarge.Error(), http.StatusRequestEntityTooLarge)
		return false
	}
	req.Body = http.MaxBytesReader(w, req.Body, maxScheduleBodyBytes)
	if err := json.NewDecoder(req.Body).Decode(into); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			http.Error(w, errBodyTooLarge.Error(), http.StatusRequestEntityTooLarge)
			return false
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// normalizedTimezone keeps an empty timezone working (UTC) instead of failing
// every schedule written by a frontend that leaves the field alone.
func normalizedTimezone(tz string) string {
	tz = strings.TrimSpace(tz)
	if tz == "" {
		return "UTC"
	}
	return tz
}

func (a *App) handleSchedules(w http.ResponseWriter, req *http.Request) {
	defer recoverHTTP(w)
	if !a.adminOnly(w, req) {
		return
	}
	if a.sched == nil {
		http.Error(w, errNoStore.Error(), http.StatusServiceUnavailable)
		return
	}
	orgID := backend.PluginConfigFromContext(req.Context()).OrgID
	switch req.Method {
	case http.MethodGet:
		a.listSchedules(w, req, orgID)
	case http.MethodPut:
		a.putSchedule(w, req, orgID)
	case http.MethodDelete:
		a.deleteSchedule(w, req, orgID)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *App) listSchedules(w http.ResponseWriter, req *http.Request, orgID int64) {
	rows, err := a.sched.List(req.Context(), orgID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]scheduleDTO, 0, len(rows))
	for _, row := range rows {
		out = append(out, toScheduleDTO(row))
	}
	writeJSON(w, out)
}

func (a *App) putSchedule(w http.ResponseWriter, req *http.Request, orgID int64) {
	var body scheduleDTO
	if !decodeBody(w, req, &body) {
		return
	}
	if body.Scope != scopePanel && body.Scope != scopeBaseline {
		http.Error(w, errInvalidScope.Error(), http.StatusBadRequest)
		return
	}
	if body.Key == "" {
		http.Error(w, errScopeKeyRequired.Error(), http.StatusBadRequest)
		return
	}
	timezone, next, err := scheduleTimes(body.Cron, body.Timezone)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// A baseline row belongs to the worker, which derives it from the metrics it
	// sees: an admin may retime an existing one, never invent one. A row created
	// here would be claimed by the fleet forever for a hash with no series, and
	// nothing on the page could remove it.
	if body.Scope == scopeBaseline {
		if _, ok, err := a.sched.Row(req.Context(), orgID, scopeBaseline, body.Key); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		} else if !ok {
			http.Error(w, errBaselineUnknown.Error(), http.StatusNotFound)
			return
		}
	}
	// No spec: an admin edit changes when a panel retrains, never how. Upsert
	// keeps the stored spec when the incoming one is empty.
	err = a.sched.Upsert(req.Context(), orgID, ScheduleRow{
		Scope:     body.Scope,
		Key:       body.Key,
		Cron:      body.Cron,
		Timezone:  timezone,
		Enabled:   body.Enabled,
		NextRunAt: next,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	row, ok, err := a.sched.Row(req.Context(), orgID, body.Scope, body.Key)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if ok {
		writeJSON(w, toScheduleDTO(row))
		return
	}
	// Unreachable: the upsert just wrote the row. Answer the caller with what it
	// asked for rather than an error it cannot act on.
	writeJSON(w, scheduleDTO{
		Scope:     body.Scope,
		Key:       body.Key,
		Cron:      body.Cron,
		Timezone:  timezone,
		Enabled:   body.Enabled,
		NextRunAt: next.UTC().Format(time.RFC3339),
	})
}

// deleteSchedule removes one row. Panel rows are this org's; a baseline row is
// fleet-wide (org 0) and the worker re-creates it on its next tick if the hash is
// still reporting, so deleting a retired hash's row sticks and deleting a live
// one is harmless.
func (a *App) deleteSchedule(w http.ResponseWriter, req *http.Request, orgID int64) {
	query := req.URL.Query()
	scope, key := query.Get("scope"), query.Get("key")
	if scope == "" || key == "" {
		http.Error(w, errScopeKeyRequired.Error(), http.StatusBadRequest)
		return
	}
	if scope != scopePanel && scope != scopeBaseline {
		http.Error(w, errInvalidScope.Error(), http.StatusBadRequest)
		return
	}
	if err := a.sched.Delete(req.Context(), orgID, scope, key); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"message": "ok"})
}

// handleScheduleDefault validates the org's default schedule before the
// Configuration page saves it into jsonData: the backend is the only place that
// can prove the expression parses, and the UI cannot undo a saved bad default.
func (a *App) handleScheduleDefault(w http.ResponseWriter, req *http.Request) {
	defer recoverHTTP(w)
	if !a.adminOnly(w, req) {
		return
	}
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Cron     string `json:"cron"`
		Timezone string `json:"timezone"`
	}
	if !decodeBody(w, req, &body) {
		return
	}
	timezone, _, err := scheduleTimes(body.Cron, body.Timezone)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]string{"cron": body.Cron, "timezone": timezone})
}

// scheduleTimes validates a cron/timezone pair and resolves its next run.
func scheduleTimes(cronSpec, timezone string) (string, time.Time, error) {
	timezone = normalizedTimezone(timezone)
	next, err := nextRun(cronSpec, timezone, time.Now())
	if err != nil {
		return "", time.Time{}, err
	}
	return timezone, next, nil
}
