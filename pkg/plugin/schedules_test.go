package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

// schedKey is the primary key of forecast.retrain: (scope, org_id, key). A panel's
// cache key is org-independent, so two orgs can hold the same key as two rows.
func schedKey(scope string, orgID int64, key string) string {
	return scope + "\x00" + strconv.FormatInt(orgID, 10) + "\x00" + key
}

type finishCall struct {
	Owner  string
	Scope  string
	Key    string
	Next   time.Time
	Status string
}

// schedLease is one claimed row's holder and expiry, the in-memory twin of the
// (claimed_by, claimed_until) pair.
type schedLease struct {
	owner string
	until time.Time
}

// memSchedules is an in-memory ScheduleStore. It applies the same
// enabled/spec/lease rules as the SQL claim predicate, so tests that go through
// it exercise the scheduling semantics rather than a stub.
type memSchedules struct {
	mu     sync.Mutex
	rows   map[string]ScheduleRow
	leases map[string]schedLease
	finish []finishCall
}

var _ ScheduleStore = (*memSchedules)(nil)

func newMemSchedules() *memSchedules {
	return &memSchedules{rows: map[string]ScheduleRow{}, leases: map[string]schedLease{}}
}

// List mirrors the SQL predicate: baseline rows are fleet-wide (the worker
// writes them with no org), so they are visible to every org.
func (m *memSchedules) List(_ context.Context, orgID int64) ([]ScheduleRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ScheduleRow, 0, len(m.rows))
	for _, row := range m.rows {
		if row.Scope == scopeBaseline || row.OrgID == orgID {
			out = append(out, row)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return schedKey(out[i].Scope, out[i].OrgID, out[i].Key) < schedKey(out[j].Scope, out[j].OrgID, out[j].Key)
	})
	return out, nil
}

// Upsert mirrors the SQL statement: the conflict target is the full primary key
// (scope, org_id, key), so another org's row with the same cache key is a different
// row and never an update of this one. An absent spec and the run history survive,
// everything else is replaced. Baseline rows are stored at org 0 wherever they are
// written from, as the store's own forcing does. superseded_at is never written
// here either: an update keeps it, so a retrain cannot resurrect a retired row.
func (m *memSchedules) Upsert(_ context.Context, orgID int64, row ScheduleRow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if row.Scope == scopeBaseline {
		orgID = 0
	}
	k := schedKey(row.Scope, orgID, row.Key)
	if old, ok := m.rows[k]; ok {
		if len(row.Spec) == 0 {
			row.Spec = old.Spec
		}
		row.LastRunAt, row.LastStatus = old.LastRunAt, old.LastStatus
		row.SupersededAt = old.SupersededAt
	}
	row.OrgID = orgID
	m.rows[k] = row
	return nil
}

// Supersede mirrors supersedeSQL: the rows a panel's other cache keys trained are
// retired, keepKey's own flag is cleared, and rows without that provenance (or
// another panel's) are untouched.
func (m *memSchedules) Supersede(_ context.Context, orgID int64, dashboardUID string, panelID int, keepKey string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, row := range m.rows {
		if row.Scope != scopePanel || row.OrgID != orgID {
			continue
		}
		var spec struct {
			DashboardUID string `json:"dashboardUid"`
			PanelID      int    `json:"panelId"`
		}
		if err := json.Unmarshal(row.Spec, &spec); err != nil {
			continue
		}
		if spec.DashboardUID != dashboardUID || spec.PanelID != panelID {
			continue
		}
		switch {
		case row.Key == keepKey && !row.SupersededAt.IsZero():
			row.SupersededAt = time.Time{}
		case row.Key != keepKey && row.SupersededAt.IsZero():
			row.SupersededAt = time.Now()
		default:
			continue
		}
		m.rows[k] = row
	}
	return nil
}

// Row mirrors the SQL lookup: one row by its full key.
func (m *memSchedules) Row(_ context.Context, orgID int64, scope, key string) (ScheduleRow, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if scope == scopeBaseline {
		orgID = 0
	}
	row, ok := m.rows[schedKey(scope, orgID, key)]
	return row, ok, nil
}

func (m *memSchedules) Delete(_ context.Context, orgID int64, scope, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if scope == scopeBaseline {
		orgID = 0
	}
	delete(m.rows, schedKey(scope, orgID, key))
	return nil
}

// Identify mirrors identifySQL: a merge into an existing panel spec, never a new
// row, never a spec Postgres would refuse to merge into, and never a write when the
// values are already there.
func (m *memSchedules) Identify(_ context.Context, orgID int64, key string, prov PanelProvenance) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := schedKey(scopePanel, orgID, key)
	row, ok := m.rows[k]
	if !ok || len(row.Spec) == 0 {
		return nil
	}
	var spec map[string]any
	if err := json.Unmarshal(row.Spec, &spec); err != nil {
		return nil
	}
	patch, err := provenanceJSON(prov)
	if err != nil {
		return err
	}
	var fields map[string]any
	if err := json.Unmarshal(patch, &fields); err != nil {
		return err
	}
	same := func(a, b any) bool {
		ja, errA := json.Marshal(a)
		jb, errB := json.Marshal(b)
		return errA == nil && errB == nil && string(ja) == string(jb)
	}
	changed := false
	for name, value := range fields {
		if !same(spec[name], value) {
			changed = true
		}
		spec[name] = value
	}
	if !changed {
		return nil
	}
	merged, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	row.Spec = merged
	m.rows[k] = row
	return nil
}

func (m *memSchedules) Due(_ context.Context, orgID int64, key string, now time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.rows[schedKey(scopePanel, orgID, key)]
	return ok && row.Enabled && !row.NextRunAt.IsZero() && !row.NextRunAt.After(now), nil
}

// specHasQueries is the in-memory twin of panelClaimSQL's jsonb predicates: a
// claimable row's spec must carry a non-empty JSON array of queries.
func specHasQueries(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	var spec struct {
		Queries json.RawMessage `json:"queries"`
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		return false
	}
	return hasQueries(spec.Queries)
}

// Claim mirrors panelClaimSQL: panel rows of this org only, enabled, with a
// non-empty query array, not superseded, due, and not leased by anyone else.
func (m *memSchedules) Claim(_ context.Context, orgID int64, owner string, lease time.Duration, limit int) ([]ScheduleRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	due := make([]ScheduleRow, 0, limit)
	for _, row := range m.rows {
		if row.OrgID != orgID || row.Scope != scopePanel || !row.Enabled || !specHasQueries(row.Spec) {
			continue
		}
		if !row.SupersededAt.IsZero() {
			continue
		}
		if row.NextRunAt.IsZero() || row.NextRunAt.After(now) {
			continue
		}
		if lease, ok := m.leases[schedKey(row.Scope, row.OrgID, row.Key)]; ok && lease.until.After(now) {
			continue
		}
		due = append(due, row)
	}
	sort.Slice(due, func(i, j int) bool { return due[i].NextRunAt.Before(due[j].NextRunAt) })
	if len(due) > limit {
		due = due[:limit]
	}
	for _, row := range due {
		m.leases[schedKey(row.Scope, row.OrgID, row.Key)] = schedLease{owner: owner, until: now.Add(lease)}
	}
	return due, nil
}

// Finish mirrors the SQL's owner predicate: only the holder of the claim may
// release it or write its outcome, so a retrain that outlived its lease leaves the
// newer owner's claim, next run and status alone.
func (m *memSchedules) Finish(_ context.Context, owner string, orgID int64, scope, key string, next time.Time, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.finish = append(m.finish, finishCall{Owner: owner, Scope: scope, Key: key, Next: next, Status: status})
	k := schedKey(scope, orgID, key)
	if lease, ok := m.leases[k]; !ok || lease.owner != owner {
		return nil
	}
	row := m.rows[k]
	row.Scope, row.Key, row.OrgID = scope, key, orgID
	row.NextRunAt, row.LastRunAt, row.LastStatus = next, time.Now(), status
	m.rows[k] = row
	delete(m.leases, k)
	return nil
}

func (m *memSchedules) finished() []finishCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]finishCall(nil), m.finish...)
}

func (m *memSchedules) seed(row ScheduleRow) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[schedKey(row.Scope, row.OrgID, row.Key)] = row
}

func adminCtx(orgID int64) backend.PluginContext {
	return backend.PluginContext{OrgID: orgID, User: &backend.User{Role: "Admin"}}
}

func viewerCtx(orgID int64) backend.PluginContext {
	return backend.PluginContext{OrgID: orgID, User: &backend.User{Role: "Viewer"}}
}

func callRoute(t *testing.T, app *App, pCtx backend.PluginContext, method, path string, body []byte) (int, []byte) {
	t.Helper()
	var r mockCallResourceResponseSender
	err := app.CallResource(context.Background(), &backend.CallResourceRequest{
		PluginContext: pCtx,
		Method:        method,
		Path:          path,
		Body:          body,
	}, &r)
	if err != nil {
		t.Fatalf("CallResource: %v", err)
	}
	if r.response == nil {
		t.Fatal("no response")
	}
	return r.response.Status, r.response.Body
}

func schedulesApp(t *testing.T, sched ScheduleStore) *App {
	t.Helper()
	clearStoreEnv(t)
	app, err := newApp(context.Background(), backend.AppInstanceSettings{}, newMemoryStore(), sched, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Dispose)
	return app
}

// TestScheduleDropParameter: `drop` says how far a delete goes. The default keeps
// v12's contract (the row, not the model), an unknown value is refused before
// anything is deleted, and a baseline key's model is not this process's to remove —
// the row goes and the response says where the model lives.
func TestScheduleDropParameter(t *testing.T) {
	ctx := context.Background()
	sched := newMemSchedules()
	for _, key := range []string{"panel-a", "panel-b"} {
		sched.seed(ScheduleRow{OrgID: 1, Scope: scopePanel, Key: key, Cron: "0 3 * * *", Timezone: "UTC", Enabled: true})
	}
	sched.seed(ScheduleRow{OrgID: 0, Scope: scopeBaseline, Key: "ready", Cron: "0 3 * * *", Timezone: "UTC", Enabled: true})
	app := schedulesApp(t, sched)

	// The refusals come first, while both rows are still there: an invalid parameter
	// must not delete anything on its way to a 400.
	for _, tc := range []struct {
		name string
		path string
		pCtx backend.PluginContext
		want int
	}{
		{
			name: "an unknown value is refused",
			path: "schedules?scope=panel&key=panel-a&drop=everything",
			pCtx: adminCtx(1),
			want: http.StatusBadRequest,
		},
		{
			name: "a viewer cannot drop a model",
			path: "schedules?scope=panel&key=panel-a&drop=model",
			pCtx: viewerCtx(1),
			want: http.StatusForbidden,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, body := callRoute(t, app, tc.pCtx, http.MethodDelete, tc.path, nil)
			if status != tc.want {
				t.Fatalf("status=%d want %d body=%s", status, tc.want, body)
			}
			if _, ok, err := sched.Row(ctx, 1, scopePanel, "panel-a"); err != nil || !ok {
				t.Fatalf("a refused delete removed the row: ok=%v err=%v", ok, err)
			}
		})
	}

	// The default deletes the row and keeps the model: the model is what the panel's
	// cacheKey resolves to, so stopping the unattended retrain must not throw it away.
	status, body := callRoute(t, app, adminCtx(1), http.MethodDelete, "schedules?scope=panel&key=panel-a", nil)
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	if _, ok, err := sched.Row(ctx, 1, scopePanel, "panel-a"); err != nil || ok {
		t.Fatalf("the row survived: ok=%v err=%v", ok, err)
	}
	// `drop=row` is the same request spelled out.
	status, body = callRoute(t, app, adminCtx(1), http.MethodDelete, "schedules?scope=panel&key=panel-b&drop=row", nil)
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	if _, ok, err := sched.Row(ctx, 1, scopePanel, "panel-b"); err != nil || ok {
		t.Fatalf("the row survived: ok=%v err=%v", ok, err)
	}

	// A baseline key: the row goes, and the response says the model is the worker's.
	status, body = callRoute(t, app, adminCtx(1), http.MethodDelete, "schedules?scope=baseline&key=ready&drop=model", nil)
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	if !strings.Contains(string(body), "baselines worker") {
		t.Fatalf("the response does not say where the model is: %s", body)
	}
	if _, ok, err := sched.Row(ctx, 0, scopeBaseline, "ready"); err != nil || ok {
		t.Fatalf("the baseline row survived: ok=%v err=%v", ok, err)
	}
}

// TestPostgresDropModelRoute is the model half of `drop=model` against a real
// database: the snapshot and the row go together, so the tick's reconcile cannot
// hand a row back to a model the caller removed — while the default still leaves
// the model in place (v12's contract) and lets the reconcile restore the row.
func TestPostgresDropModelRoute(t *testing.T) {
	clearRetrainEnv(t)
	clearStoreEnv(t)
	ctx := context.Background()
	s := scratchPostgresStore(t, "forecast_drop_model")
	app, err := newApp(ctx, backend.AppInstanceSettings{}, s, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Dispose)

	const orgID int64 = 14
	key := strings.Repeat("a", 64)
	seed := func() {
		t.Helper()
		putSnapshot(t, ctx, s, orgID, key)
		if err := s.Upsert(ctx, orgID, ScheduleRow{
			Scope: scopePanel, Key: key, Cron: "*/7 * * * *", Timezone: "UTC", Enabled: true,
			Spec: json.RawMessage(`{"queries":[{"refId":"A"}]}`), NextRunAt: time.Now().Add(time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
	}

	seed()
	status, body := callRoute(t, app, adminCtx(orgID), http.MethodDelete, "schedules?scope=panel&key="+key+"&drop=model", nil)
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	if _, ok, err := s.Get(ctx, orgID, key); err != nil || ok {
		t.Fatalf("the model survived drop=model: ok=%v err=%v", ok, err)
	}
	if _, ok, err := s.Row(ctx, orgID, scopePanel, key); err != nil || ok {
		t.Fatalf("the row survived drop=model: ok=%v err=%v", ok, err)
	}
	if res := sweep(t, ctx, s); res != (sweepResult{}) {
		t.Fatalf("the sweep resurrected the removed model: %+v", res)
	}

	seed()
	status, body = callRoute(t, app, adminCtx(orgID), http.MethodDelete, "schedules?scope=panel&key="+key, nil)
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	if _, ok, err := s.Get(ctx, orgID, key); err != nil || !ok {
		t.Fatalf("drop=row removed the model: ok=%v err=%v", ok, err)
	}
	if res := sweep(t, ctx, s); res.Recreated != 1 {
		t.Fatalf("drop=row left the model orphaned: %+v", res)
	}
	row, ok, err := s.Row(ctx, orgID, scopePanel, key)
	if err != nil || !ok {
		t.Fatalf("the sweep did not give the row back: ok=%v err=%v", ok, err)
	}
	if len(row.Spec) != 0 || row.Cron != "0 3 * * *" {
		t.Fatalf("the re-created row is not the unclaimable default: %+v", row)
	}
}

// TestScheduleRoutesBodyCap: a schedule DTO is a few hundred bytes, so both routes
// refuse a body above the cap — the same reason and status /forecast answers, on the
// routes that carry a body of their own.
func TestScheduleRoutesBodyCap(t *testing.T) {
	app := schedulesApp(t, newMemSchedules())
	body := []byte(`{"scope":"panel","key":"k","cron":"0 3 * * *","pad":"` +
		strings.Repeat("x", maxScheduleBodyBytes) + `"}`)
	for _, tc := range []struct {
		name, method, path string
	}{
		{name: "PUT a schedule", method: http.MethodPut, path: "schedules"},
		{name: "POST the default", method: http.MethodPost, path: "schedules/default"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, got := callRoute(t, app, adminCtx(1), tc.method, tc.path, body)
			if status != http.StatusRequestEntityTooLarge {
				t.Fatalf("status=%d body=%s", status, got)
			}
			if !strings.Contains(string(got), errBodyTooLarge.Error()) {
				t.Fatalf("body=%q want the cap's own reason", got)
			}
		})
	}
}

// rowErrorSchedules fails only the single-row read, so a fit's fallback behaviour
// can be exercised while the rest of the schedule store keeps working.
type rowErrorSchedules struct {
	ScheduleStore
	err     error
	upserts int
}

func (s *rowErrorSchedules) Row(context.Context, int64, string, string) (ScheduleRow, bool, error) {
	return ScheduleRow{}, false, s.err
}

func (s *rowErrorSchedules) Upsert(ctx context.Context, orgID int64, row ScheduleRow) error {
	s.upserts++
	return s.ScheduleStore.Upsert(ctx, orgID, row)
}

// TestClampCellText: one cap covers every cell-sized string the schedule response
// carries, counted in runes so a multi-byte field is not cut mid-character.
func TestClampCellText(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: ""},
		{name: "short", in: "CPU", want: "CPU"},
		{name: "exactly the cap", in: strings.Repeat("x", maxQuerySummary), want: strings.Repeat("x", maxQuerySummary)},
		{name: "one over the cap", in: strings.Repeat("x", maxQuerySummary+1), want: strings.Repeat("x", maxQuerySummary-1) + "…"},
		{name: "multi-byte counts in runes", in: strings.Repeat("ж", maxQuerySummary), want: strings.Repeat("ж", maxQuerySummary)},
		{name: "multi-byte over the cap", in: strings.Repeat("ж", maxQuerySummary+1), want: strings.Repeat("ж", maxQuerySummary-1) + "…"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := clampCellText(tc.in)
			if got != tc.want {
				t.Fatalf("in=%q got=%q want=%q", tc.name, got, tc.want)
			}
			if n := len([]rune(got)); n > maxQuerySummary {
				t.Fatalf("clamped to %d runes", n)
			}
		})
	}
}

// TestForecastFitSurvivesAScheduleRowError: a fit that could not read its row must
// not write the deployment defaults over it. An admin's cron and enable state
// exist only in that row, so falling through would silently re-enable a schedule
// an admin disabled and reset when it fires. The snapshot the user asked for is
// stored either way, so the fit itself still reports ok.
func TestForecastFitSurvivesAScheduleRowError(t *testing.T) {
	clearRetrainEnv(t)
	clearStoreEnv(t)
	ctx := context.Background()
	key := strings.Repeat("34", 32)
	sched := newMemSchedules()
	sched.seed(ScheduleRow{
		OrgID: 5, Scope: scopePanel, Key: key, Cron: "*/2 * * * *", Timezone: "Europe/Moscow",
		Enabled: false, Spec: json.RawMessage(`{"queries":[{"refId":"A"}]}`),
	})
	broken := &rowErrorSchedules{ScheduleStore: sched, err: errors.New("forecast store: connection reset by peer")}
	snaps := newMemoryStore()
	app, err := newApp(ctx, backend.AppInstanceSettings{}, snaps, broken, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Dispose)

	body, _ := json.Marshal(ForecastRequest{
		Times:  []int64{0, 1000, 2000, 3000},
		Values: []nullableFloat{1, 2, 3, 4},
		Model:  "naive", From: 4000, To: 5000, CacheKey: key,
		TrainSource: &TrainSource{
			DatasourceUID: "ds-uid", Queries: json.RawMessage(`[{"refId":"A"}]`),
			From: 1, To: 2, SeriesName: "s",
		},
	})
	var r mockCallResourceResponseSender
	if err := app.CallResource(ctx, &backend.CallResourceRequest{
		PluginContext: backend.PluginContext{OrgID: 5},
		Method:        http.MethodPost,
		Path:          "forecast",
		Body:          body,
	}, &r); err != nil {
		t.Fatal(err)
	}
	if r.response.Status != http.StatusOK {
		t.Fatalf("status=%d body=%s", r.response.Status, r.response.Body)
	}
	if broken.upserts != 0 {
		t.Fatalf("a failed schedule read rewrote the row (%d writes)", broken.upserts)
	}
	row, ok, err := sched.Row(ctx, 5, scopePanel, key)
	if err != nil || !ok {
		t.Fatalf("row: ok=%v err=%v", ok, err)
	}
	if row.Cron != "*/2 * * * *" || row.Timezone != "Europe/Moscow" || row.Enabled {
		t.Fatalf("the fit rewrote the admin's row: %+v", row)
	}
	if _, ok, err := snaps.Get(ctx, 5, key); err != nil || !ok {
		t.Fatalf("the fit was not stored: ok=%v err=%v", ok, err)
	}
}

func TestScheduleRoutesAdminOnly(t *testing.T) {
	app := schedulesApp(t, newMemSchedules())
	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   []byte
	}{
		{name: "list", method: http.MethodGet, path: "schedules"},
		{name: "upsert", method: http.MethodPut, path: "schedules", body: []byte(`{"scope":"panel","key":"k","cron":"0 3 * * *"}`)},
		{name: "delete", method: http.MethodDelete, path: "schedules?scope=panel&key=k"},
		{name: "default", method: http.MethodPost, path: "schedules/default", body: []byte(`{"cron":"0 3 * * *"}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, body := callRoute(t, app, viewerCtx(1), tc.method, tc.path, tc.body)
			if status != http.StatusForbidden {
				t.Fatalf("viewer status=%d body=%s", status, body)
			}
			if !strings.Contains(string(body), errAdminRequired.Error()) {
				t.Fatalf("body=%s", body)
			}
			// No session user at all is the anonymous case and must be refused too.
			if status, _ := callRoute(t, app, backend.PluginContext{OrgID: 1}, tc.method, tc.path, tc.body); status != http.StatusForbidden {
				t.Fatalf("anonymous status=%d", status)
			}
		})
	}
}

func TestScheduleRoutesMethodMatrix(t *testing.T) {
	app := schedulesApp(t, newMemSchedules())
	for _, tc := range []struct {
		name   string
		method string
		path   string
		want   int
	}{
		{name: "post schedules", method: http.MethodPost, path: "schedules", want: http.StatusMethodNotAllowed},
		{name: "get default", method: http.MethodGet, path: "schedules/default", want: http.StatusMethodNotAllowed},
		{name: "delete default", method: http.MethodDelete, path: "schedules/default", want: http.StatusMethodNotAllowed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if status, body := callRoute(t, app, adminCtx(1), tc.method, tc.path, nil); status != tc.want {
				t.Fatalf("status=%d want %d body=%s", status, tc.want, body)
			}
		})
	}
}

func TestScheduleRoutesWithoutStore(t *testing.T) {
	app := schedulesApp(t, nil)
	status, body := callRoute(t, app, adminCtx(1), http.MethodGet, "schedules", nil)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", status, body)
	}
}

func TestScheduleUpsertListRoundTrip(t *testing.T) {
	sched := newMemSchedules()
	sched.seed(ScheduleRow{OrgID: 1, Scope: scopePanel, Key: "seeded", Cron: "0 3 * * *", Timezone: "UTC", Enabled: true, Spec: json.RawMessage(`{"marker":"do-not-echo"}`)})
	app := schedulesApp(t, sched)

	status, body := callRoute(t, app, adminCtx(1), http.MethodPut, "schedules",
		[]byte(`{"scope":"panel","key":"panel-key","cron":"*/7 * * * *","timezone":"Europe/Moscow","enabled":true}`))
	if status != http.StatusOK {
		t.Fatalf("put status=%d body=%s", status, body)
	}
	var got scheduleDTO
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Cron != "*/7 * * * *" || got.Timezone != "Europe/Moscow" || got.Enabled == nil || !*got.Enabled {
		t.Fatalf("put dto=%+v", got)
	}
	if got.NextRunAt == "" {
		t.Fatalf("put dto has no next run: %+v", got)
	}

	status, body = callRoute(t, app, adminCtx(1), http.MethodGet, "schedules", nil)
	if status != http.StatusOK {
		t.Fatalf("list status=%d body=%s", status, body)
	}
	var rows []scheduleDTO
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows=%+v", rows)
	}
	byKey := map[string]scheduleDTO{}
	for _, row := range rows {
		byKey[row.Key] = row
	}
	if byKey["panel-key"].Cron != "*/7 * * * *" || byKey["panel-key"].HasSpec {
		t.Fatalf("panel row=%+v", byKey["panel-key"])
	}
	if !byKey["seeded"].HasSpec {
		t.Fatalf("seeded row lost its spec: %+v", byKey["seeded"])
	}
	// The stored spec is the panel's datasource query objects and never leaves
	// the backend.
	if strings.Contains(string(body), "do-not-echo") {
		t.Fatalf("list leaked spec: %s", body)
	}
}

// The worker writes its baseline rows with no org at all (the column default),
// and the Configuration page shows them next to the panel rows, so a
// worker-written row must be listed for every org.
func TestScheduleListIncludesWorkerBaselineRows(t *testing.T) {
	sched := newMemSchedules()
	sched.seed(ScheduleRow{OrgID: 0, Scope: scopeBaseline, Key: "ready", Cron: "0 3 * * *", Timezone: "UTC", Enabled: true})
	app := schedulesApp(t, sched)

	status, body := callRoute(t, app, adminCtx(1), http.MethodGet, "schedules", nil)
	if status != http.StatusOK {
		t.Fatalf("list status=%d body=%s", status, body)
	}
	var rows []scheduleDTO
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Scope != scopeBaseline || rows[0].Key != "ready" {
		t.Fatalf("worker baseline row missing from the org list: %s", body)
	}
}

// The Source column is the only thing that tells an admin which dashboard panel a
// cache hash belongs to, so it has to survive every spec shape the store can hold —
// and it must never become a back door for the stored query objects.
func TestScheduleListSourceSummary(t *testing.T) {
	long := strings.Repeat("x", 300)
	// capped is the expected clamp, spelled out rather than taken from the code
	// under test (clampCellText has its own table).
	capped := func(s string) string {
		r := []rune(s)
		return string(r[:maxQuerySummary-1]) + "…"
	}
	for _, tc := range []struct {
		name     string
		row      ScheduleRow
		want     *scheduleSourceDTO
		wantLast string
		wantGone []string
	}{
		{
			name: "provenance is summarised, the spec is not echoed",
			row: ScheduleRow{OrgID: 1, Scope: scopePanel, Key: "panel-a", Cron: "0 3 * * *", Timezone: "UTC", Enabled: true,
				Spec: json.RawMessage(`{"datasourceUid":"prom","queries":[{"refId":"A","expr":"marker-only-in-the-spec"}],"from":1700000000000,"to":1700100000000,"seriesName":"up","lookback":"21d","panelId":7,"panelTitle":"CPU","dashboardUid":"dash-1","querySummary":"PromQL: up"}`)},
			want: &scheduleSourceDTO{
				DashboardUID: "dash-1", PanelID: 7, PanelTitle: "CPU",
				DatasourceUID: "prom", SeriesName: "up", QuerySummary: "PromQL: up", Lookback: "21d",
			},
			wantGone: []string{`"queries"`, "marker-only-in-the-spec", `"from"`},
		},
		{
			// A row written before the frontend recorded provenance still names its
			// series, which is most of what identifies it.
			name: "a spec without provenance still identifies its series",
			row: ScheduleRow{OrgID: 1, Scope: scopePanel, Key: "panel-b", Cron: "0 3 * * *", Timezone: "UTC", Enabled: true,
				Spec: json.RawMessage(`{"datasourceUid":"prom","queries":[{"refId":"A"}],"from":1,"to":2,"seriesName":"up","lookback":"21d"}`)},
			want: &scheduleSourceDTO{DatasourceUID: "prom", SeriesName: "up", Lookback: "21d"},
		},
		{
			// The listing is an admin page, not the scheduler: a spec the scheduler
			// would refuse must not empty the table.
			name: "a malformed spec costs one row its source",
			row: ScheduleRow{OrgID: 1, Scope: scopePanel, Key: "panel-c", Cron: "0 3 * * *", Timezone: "UTC", Enabled: true,
				Spec: json.RawMessage(`{"queries":`)},
		},
		{
			// A spec the worker might write for a baseline row carries no identity at
			// all: the row's key is the metric hash.
			name: "a spec with no identification keys has no source",
			row: ScheduleRow{OrgID: 1, Scope: scopePanel, Key: "panel-d", Cron: "0 3 * * *", Timezone: "UTC", Enabled: true,
				Spec: json.RawMessage(`{"model":"baseline","season":"minute-week"}`)},
		},
		{
			name: "a worker baseline row has no spec to summarise",
			row:  ScheduleRow{OrgID: 0, Scope: scopeBaseline, Key: "ready", Cron: "*/5 * * * *", Timezone: "UTC", Enabled: true},
		},
		{
			name: "a stored summary is capped before it reaches the table",
			row: ScheduleRow{OrgID: 1, Scope: scopePanel, Key: "panel-e", Cron: "0 3 * * *", Timezone: "UTC", Enabled: true,
				Spec: json.RawMessage(`{"querySummary":"` + long + `"}`)},
			want: &scheduleSourceDTO{QuerySummary: strings.Repeat("x", maxQuerySummary-1) + "…"},
		},
		{
			// Every cell-sized string of the spec is capped, not just the summary: a
			// panel title can be as long as the client that stored it wants.
			name: "a long panel title is capped",
			row: ScheduleRow{OrgID: 1, Scope: scopePanel, Key: "panel-f", Cron: "0 3 * * *", Timezone: "UTC", Enabled: true,
				Spec: json.RawMessage(`{"panelTitle":"` + long + `","seriesName":"up"}`)},
			want: &scheduleSourceDTO{PanelTitle: capped(long), SeriesName: "up"},
		},
		{
			// lastStatus is an error message, and an error message can embed a series
			// name as long as its datasource allows: the response caps it too.
			name: "a stored error message is capped",
			row: ScheduleRow{OrgID: 1, Scope: scopePanel, Key: "panel-g", Cron: "0 3 * * *", Timezone: "UTC", Enabled: true,
				LastStatus: "error: " + long},
			wantLast: capped("error: " + long),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sched := newMemSchedules()
			sched.seed(tc.row)
			app := schedulesApp(t, sched)
			// Ask as another org when the row is a worker baseline: those are fleet-wide,
			// the rest are this org's.
			org := int64(1)
			if tc.row.Scope == scopeBaseline {
				org = 2
			}
			status, body := callRoute(t, app, adminCtx(org), http.MethodGet, "schedules", nil)
			if status != http.StatusOK {
				t.Fatalf("list status=%d body=%s", status, body)
			}
			var rows []scheduleDTO
			if err := json.Unmarshal(body, &rows); err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 {
				t.Fatalf("rows=%s", body)
			}
			got := rows[0]
			switch {
			case tc.want == nil && got.Source != nil:
				t.Fatalf("source=%+v want none", got.Source)
			case tc.want != nil && got.Source == nil:
				t.Fatalf("no source, want %+v", tc.want)
			case tc.want != nil && *got.Source != *tc.want:
				t.Fatalf("source=%+v want %+v", *got.Source, *tc.want)
			}
			if got.Key != tc.row.Key || got.Cron == "" {
				t.Fatalf("row fields lost: %+v", got)
			}
			if got.HasSpec != (len(tc.row.Spec) > 0) {
				t.Fatalf("hasSpec=%v spec=%s", got.HasSpec, tc.row.Spec)
			}
			wantLast := tc.wantLast
			if wantLast == "" {
				wantLast = tc.row.LastStatus
			}
			if got.LastStatus != wantLast {
				t.Fatalf("lastStatus=%q want %q", got.LastStatus, wantLast)
			}
			for _, gone := range tc.wantGone {
				if strings.Contains(string(body), gone) {
					t.Fatalf("the list leaked %s: %s", gone, body)
				}
			}
		})
	}
}

func TestScheduleUpsertKeepsExistingSpec(t *testing.T) {
	ctx := context.Background()
	sched := newMemSchedules()
	app := schedulesApp(t, sched)
	key := strings.Repeat("ab", 32)
	sched.seed(ScheduleRow{OrgID: 1, Scope: scopePanel, Key: key, Cron: "0 3 * * *", Timezone: "UTC", Enabled: true, Spec: json.RawMessage(`{"queries":[{"refId":"A"}]}`)})

	if status, body := callRoute(t, app, adminCtx(1), http.MethodPut, "schedules",
		[]byte(`{"scope":"panel","key":"`+key+`","cron":"*/9 * * * *","timezone":"UTC","enabled":false}`)); status != http.StatusOK {
		t.Fatalf("put status=%d body=%s", status, body)
	}
	rows, err := sched.List(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || len(rows[0].Spec) == 0 {
		t.Fatalf("spec dropped: %+v", rows)
	}
	if rows[0].Cron != "*/9 * * * *" || rows[0].Enabled {
		t.Fatalf("cron/enabled not applied: %+v", rows[0])
	}
}

// TestSchedulePutEnabledDefaults pins what an absent `enabled` means: the stored
// value on an update, and true on an insert. A plain bool field wrote false, so a
// client that only edited the cron silently switched a running schedule off.
func TestSchedulePutEnabledDefaults(t *testing.T) {
	ctx := context.Background()
	sched := newMemSchedules()
	app := schedulesApp(t, sched)
	key := strings.Repeat("4d", 32)

	// Insert without `enabled`: a new schedule is on.
	status, body := callRoute(t, app, adminCtx(1), http.MethodPut, "schedules",
		[]byte(`{"scope":"panel","key":"`+key+`","cron":"0 3 * * *","timezone":"UTC"}`))
	if status != http.StatusOK {
		t.Fatalf("insert status=%d body=%s", status, body)
	}
	var dto scheduleDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		t.Fatal(err)
	}
	if dto.Enabled == nil || !*dto.Enabled {
		t.Fatalf("insert dto=%+v", dto)
	}
	row, ok, err := sched.Row(ctx, 1, scopePanel, key)
	if err != nil || !ok || !row.Enabled {
		t.Fatalf("insert row ok=%v err=%v row=%+v", ok, err, row)
	}

	// A schedule an admin turned off stays off when the next edit leaves the field
	// out; the cron still changes.
	if err := sched.Upsert(ctx, 1, ScheduleRow{
		Scope: scopePanel, Key: key, Cron: "0 3 * * *", Timezone: "UTC",
		Enabled: false, Spec: json.RawMessage(`{"queries":[{"refId":"A"}]}`),
	}); err != nil {
		t.Fatal(err)
	}
	status, body = callRoute(t, app, adminCtx(1), http.MethodPut, "schedules",
		[]byte(`{"scope":"panel","key":"`+key+`","cron":"*/9 * * * *","timezone":"UTC"}`))
	if status != http.StatusOK {
		t.Fatalf("update status=%d body=%s", status, body)
	}
	row, _, err = sched.Row(ctx, 1, scopePanel, key)
	if err != nil {
		t.Fatal(err)
	}
	if row.Enabled || row.Cron != "*/9 * * * *" {
		t.Fatalf("an absent enabled rewrote the row: %+v", row)
	}

	// An explicit value is still honoured.
	status, body = callRoute(t, app, adminCtx(1), http.MethodPut, "schedules",
		[]byte(`{"scope":"panel","key":"`+key+`","cron":"*/9 * * * *","timezone":"UTC","enabled":true}`))
	if status != http.StatusOK {
		t.Fatalf("explicit status=%d body=%s", status, body)
	}
	if row, _, err = sched.Row(ctx, 1, scopePanel, key); err != nil || !row.Enabled {
		t.Fatalf("explicit enabled was ignored: err=%v row=%+v", err, row)
	}
}

func TestScheduleDelete(t *testing.T) {
	ctx := context.Background()
	sched := newMemSchedules()
	app := schedulesApp(t, sched)
	key := strings.Repeat("cd", 32)
	other := strings.Repeat("ef", 32)
	baseline := "deadbeef"
	sched.seed(ScheduleRow{OrgID: 1, Scope: scopePanel, Key: key, Cron: "0 3 * * *", Timezone: "UTC", Enabled: true})
	// The worker writes its rows with no org at all (the column default), which is
	// why they must still be listed and editable here.
	sched.seed(ScheduleRow{OrgID: 0, Scope: scopeBaseline, Key: baseline, Cron: "0 3 * * *", Timezone: "UTC", Enabled: true})

	// The worker's baseline rows are visible and editable through the same API.
	if status, body := callRoute(t, app, adminCtx(1), http.MethodPut, "schedules",
		[]byte(`{"scope":"baseline","key":"`+baseline+`","cron":"*/3 * * * *","timezone":"UTC","enabled":true}`)); status != http.StatusOK {
		t.Fatalf("baseline put status=%d body=%s", status, body)
	}
	rows, _ := sched.List(context.Background(), 1)
	for _, row := range rows {
		if row.Scope == scopeBaseline && row.OrgID != 0 {
			t.Fatalf("baseline row was re-homed to org %d: %+v", row.OrgID, row)
		}
	}

	// Deleting a baseline row removes the fleet-wide row. A live hash's row comes
	// back on the worker's next tick, which is what makes this the way out for a hash
	// that stopped reporting: its row is otherwise retried forever.
	status, body := callRoute(t, app, adminCtx(1), http.MethodDelete, "schedules?scope="+scopeBaseline+"&key="+baseline, nil)
	if status != http.StatusOK {
		t.Fatalf("baseline delete status=%d body=%s", status, body)
	}
	if rows, _ := sched.List(ctx, 1); len(rows) != 1 {
		t.Fatalf("baseline row survived the delete: %+v", rows)
	}

	// Deleting another org's panel row is a no-op for this org's request.
	sched.seed(ScheduleRow{OrgID: 2, Scope: scopePanel, Key: other, Cron: "0 3 * * *", Timezone: "UTC", Enabled: true})
	if status, body := callRoute(t, app, adminCtx(1), http.MethodDelete, "schedules?scope="+scopePanel+"&key="+other, nil); status != http.StatusOK {
		t.Fatalf("cross-org delete status=%d body=%s", status, body)
	}
	if rows, _ := sched.List(ctx, 2); len(rows) != 1 || rows[0].Key != other {
		t.Fatalf("another org's row was deleted: %+v", rows)
	}

	status, body = callRoute(t, app, adminCtx(1), http.MethodDelete, "schedules?scope="+scopePanel+"&key="+key, nil)
	if status != http.StatusOK {
		t.Fatalf("panel delete status=%d body=%s", status, body)
	}
	if rows, _ := sched.List(ctx, 1); len(rows) != 0 {
		t.Fatalf("panel row survived: %+v", rows)
	}
}

// TestScheduleBaselinePutNeedsAnExistingRow pins the rule that separates the two
// scopes on write: a baseline row describes work the fleet does for a hash it can
// see, so an admin may retime one but never invent one, or the fleet would query
// forever for a hash with no series and nothing on the page could remove it.
func TestScheduleBaselinePutNeedsAnExistingRow(t *testing.T) {
	sched := newMemSchedules()
	app := schedulesApp(t, sched)
	sched.seed(ScheduleRow{OrgID: 0, Scope: scopeBaseline, Key: "live", Cron: "0 3 * * *", Timezone: "UTC", Enabled: true})
	for _, tc := range []struct {
		name  string
		scope string
		key   string
		want  int
	}{
		{name: "an existing worker row can be retimed", scope: scopeBaseline, key: "live", want: http.StatusOK},
		{name: "an invented baseline row is refused", scope: scopeBaseline, key: "invented", want: http.StatusNotFound},
		{name: "a panel row may be created before the panel trains", scope: scopePanel, key: strings.Repeat("ab", 32), want: http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, body := callRoute(t, app, adminCtx(1), http.MethodPut, "schedules",
				[]byte(`{"scope":"`+tc.scope+`","key":"`+tc.key+`","cron":"*/5 * * * *","timezone":"UTC","enabled":true}`))
			if status != tc.want {
				t.Fatalf("status=%d want %d body=%s", status, tc.want, body)
			}
		})
	}
	rows, err := sched.List(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows=%+v want the worker row and the panel row only", rows)
	}
}

func TestScheduleValidation(t *testing.T) {
	app := schedulesApp(t, newMemSchedules())
	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "empty cron", method: http.MethodPut, path: "schedules", body: `{"scope":"panel","key":"k","cron":""}`},
		{name: "bad cron", method: http.MethodPut, path: "schedules", body: `{"scope":"panel","key":"k","cron":"not cron"}`},
		{name: "six fields", method: http.MethodPut, path: "schedules", body: `{"scope":"panel","key":"k","cron":"0 0 3 * * *"}`},
		{name: "bad timezone", method: http.MethodPut, path: "schedules", body: `{"scope":"panel","key":"k","cron":"0 3 * * *","timezone":"Mars/Olympus"}`},
		{name: "bad scope", method: http.MethodPut, path: "schedules", body: `{"scope":"other","key":"k","cron":"0 3 * * *"}`},
		{name: "missing key", method: http.MethodPut, path: "schedules", body: `{"scope":"panel","cron":"0 3 * * *"}`},
		{name: "delete missing params", method: http.MethodDelete, path: "schedules?scope=panel"},
		{name: "delete bad scope", method: http.MethodDelete, path: "schedules?scope=other&key=k"},
		{name: "default bad cron", method: http.MethodPost, path: "schedules/default", body: `{"cron":"nope"}`},
		{name: "default bad timezone", method: http.MethodPost, path: "schedules/default", body: `{"cron":"0 3 * * *","timezone":"Nowhere"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, body := callRoute(t, app, adminCtx(1), tc.method, tc.path, []byte(tc.body))
			if status != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", status, body)
			}
		})
	}
}

func TestScheduleDefault(t *testing.T) {
	app := schedulesApp(t, newMemSchedules())
	status, body := callRoute(t, app, adminCtx(1), http.MethodPost, "schedules/default", []byte(`{"cron":"*/5 * * * *"}`))
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	var got map[string]string
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got["cron"] != "*/5 * * * *" || got["timezone"] != "UTC" {
		t.Fatalf("default=%v", got)
	}

	status, body = callRoute(t, app, adminCtx(1), http.MethodPost, "schedules/default", []byte(`{"cron":"@daily","timezone":"Europe/Moscow"}`))
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got["timezone"] != "Europe/Moscow" {
		t.Fatalf("default=%v", got)
	}
}

// TestScheduleOrgIsolation pins the org boundary of the schedule table itself. A
// panel's cache key is org-independent — the same dashboard and datasource uids
// hash the same in every org — so (scope, key) as the whole primary key would make
// org 2's fit rewrite org 1's cron and stored query objects, and org 2 could not
// even list the row it wrote. org_id is part of the key, so each org owns its row.
func TestScheduleOrgIsolation(t *testing.T) {
	ctx := context.Background()
	shared := strings.Repeat("ab", 32)
	sched := newMemSchedules()
	app := schedulesApp(t, sched)

	// Both orgs train a panel that hashes to the same key, each with its own cron.
	for _, org := range []int64{1, 2} {
		cron := "0 3 * * *"
		if org == 2 {
			cron = "*/7 * * * *"
		}
		if status, body := callRoute(t, app, adminCtx(org), http.MethodPut, "schedules",
			[]byte(`{"scope":"panel","key":"`+shared+`","cron":"`+cron+`","enabled":true}`)); status != http.StatusOK {
			t.Fatalf("org %d put status=%d body=%s", org, status, body)
		}
	}
	for _, org := range []int64{1, 2} {
		want := "0 3 * * *"
		if org == 2 {
			want = "*/7 * * * *"
		}
		status, body := callRoute(t, app, adminCtx(org), http.MethodGet, "schedules", nil)
		if status != http.StatusOK {
			t.Fatalf("org %d list status=%d body=%s", org, status, body)
		}
		var rows []scheduleDTO
		if err := json.Unmarshal(body, &rows); err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0].Key != shared || rows[0].Cron != want {
			t.Fatalf("org %d sees %+v, want its own row with cron %q", org, rows, want)
		}
		// The store holds one row per org, each owned by the org that wrote it.
		row, ok, err := sched.Row(ctx, org, scopePanel, shared)
		if err != nil || !ok || row.OrgID != org || row.Cron != want {
			t.Fatalf("org %d row ok=%v err=%v row=%+v", org, ok, err, row)
		}
	}
}
