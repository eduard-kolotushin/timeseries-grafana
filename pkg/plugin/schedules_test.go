package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

func schedKey(scope, key string) string { return scope + "\x00" + key }

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
	sort.Slice(out, func(i, j int) bool { return schedKey(out[i].Scope, out[i].Key) < schedKey(out[j].Scope, out[j].Key) })
	return out, nil
}

// Upsert mirrors the UPDATE column list of the SQL statement: an absent spec and
// the run history survive, everything else is replaced.
func (m *memSchedules) Upsert(_ context.Context, orgID int64, row ScheduleRow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := schedKey(row.Scope, row.Key)
	if old, ok := m.rows[k]; ok {
		if len(row.Spec) == 0 {
			row.Spec = old.Spec
		}
		row.LastRunAt, row.LastStatus = old.LastRunAt, old.LastStatus
	}
	if row.Scope == scopeBaseline {
		orgID = 0
	}
	row.OrgID = orgID
	m.rows[k] = row
	return nil
}

func (m *memSchedules) Delete(_ context.Context, orgID int64, scope, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := schedKey(scope, key)
	if row, ok := m.rows[k]; ok && row.OrgID == orgID {
		delete(m.rows, k)
	}
	return nil
}

// Identify mirrors identifySQL: a merge into an existing panel spec, never a new
// row, never a spec Postgres would refuse to merge into, and never a write when the
// values are already there.
func (m *memSchedules) Identify(_ context.Context, orgID int64, key string, prov PanelProvenance) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := schedKey(scopePanel, key)
	row, ok := m.rows[k]
	if !ok || row.OrgID != orgID || len(row.Spec) == 0 {
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
	row, ok := m.rows[schedKey(scopePanel, key)]
	return ok && row.OrgID == orgID && row.Enabled && !row.NextRunAt.IsZero() && !row.NextRunAt.After(now), nil
}

func (m *memSchedules) Claim(_ context.Context, owner string, lease time.Duration, limit int) ([]ScheduleRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	due := make([]ScheduleRow, 0, limit)
	for _, row := range m.rows {
		k := schedKey(row.Scope, row.Key)
		if row.Scope != scopePanel || !row.Enabled || len(row.Spec) == 0 {
			continue
		}
		if row.NextRunAt.IsZero() || row.NextRunAt.After(now) {
			continue
		}
		if lease, ok := m.leases[k]; ok && lease.until.After(now) {
			continue
		}
		due = append(due, row)
	}
	sort.Slice(due, func(i, j int) bool { return due[i].NextRunAt.Before(due[j].NextRunAt) })
	if len(due) > limit {
		due = due[:limit]
	}
	for _, row := range due {
		m.leases[schedKey(row.Scope, row.Key)] = schedLease{owner: owner, until: now.Add(lease)}
	}
	return due, nil
}

// Finish mirrors the SQL's owner predicate: only the holder may release the
// claim, so a retrain that outlived its lease leaves the newer owner's claim and
// next run alone.
func (m *memSchedules) Finish(_ context.Context, owner, scope, key string, next time.Time, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.finish = append(m.finish, finishCall{Owner: owner, Scope: scope, Key: key, Next: next, Status: status})
	k := schedKey(scope, key)
	if lease, ok := m.leases[k]; ok && lease.owner != owner {
		return nil
	}
	row := m.rows[k]
	row.Scope, row.Key = scope, key
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
	m.rows[schedKey(row.Scope, row.Key)] = row
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
	if got.Cron != "*/7 * * * *" || got.Timezone != "Europe/Moscow" || !got.Enabled {
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
	for _, tc := range []struct {
		name     string
		row      ScheduleRow
		want     *scheduleSourceDTO
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

func TestScheduleDelete(t *testing.T) {
	ctx := context.Background()
	sched := newMemSchedules()
	app := schedulesApp(t, sched)
	key := strings.Repeat("cd", 32)
	baseline := "deadbeef"
	sched.seed(ScheduleRow{OrgID: 1, Scope: scopePanel, Key: key, Cron: "0 3 * * *", Timezone: "UTC", Enabled: true})
	// The worker writes its rows with no org at all (the column default), which is
	// why they must still be listed and editable here.
	sched.seed(ScheduleRow{OrgID: 0, Scope: scopeBaseline, Key: baseline, Cron: "0 3 * * *", Timezone: "UTC", Enabled: true})

	// The worker's baseline rows are visible and editable through the same API;
	// only deleting them is refused.
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

	status, body := callRoute(t, app, adminCtx(1), http.MethodDelete, "schedules?scope="+scopeBaseline+"&key="+baseline, nil)
	if status != http.StatusBadRequest || !strings.Contains(string(body), errBaselineDelete.Error()) {
		t.Fatalf("baseline delete status=%d body=%s", status, body)
	}
	if rows, _ := sched.List(ctx, 1); len(rows) != 2 {
		t.Fatalf("baseline row was deleted: %+v", rows)
	}

	status, body = callRoute(t, app, adminCtx(1), http.MethodDelete, "schedules?scope="+scopePanel+"&key="+key, nil)
	if status != http.StatusOK {
		t.Fatalf("panel delete status=%d body=%s", status, body)
	}
	rows, _ = sched.List(ctx, 1)
	if len(rows) != 1 || rows[0].Scope != scopeBaseline {
		t.Fatalf("panel row survived: %+v", rows)
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

func TestScheduleOrgIsolation(t *testing.T) {
	app := schedulesApp(t, newMemSchedules())
	if status, body := callRoute(t, app, adminCtx(1), http.MethodPut, "schedules",
		[]byte(`{"scope":"panel","key":"k","cron":"0 3 * * *","enabled":true}`)); status != http.StatusOK {
		t.Fatalf("put status=%d body=%s", status, body)
	}
	status, body := callRoute(t, app, adminCtx(2), http.MethodGet, "schedules", nil)
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	var rows []scheduleDTO
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("cross-org rows=%+v", rows)
	}
}
