package plugin

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

// TestAppCheckHealth: the app's probe answers for the store it depends on. A
// deployment without a DSN stays healthy — the overlay trains without one — while
// a store that cannot be reached reports the error that stopped it.
func TestAppCheckHealth(t *testing.T) {
	clearStoreEnv(t)
	boom := errors.New("forecast store: dial tcp 127.0.0.1:1: connect: connection refused")
	for _, tc := range []struct {
		name    string
		store   SnapshotStore
		want    backend.HealthStatus
		wantMsg string
	}{
		{name: "no store configured", store: nil, want: backend.HealthStatusOk, wantMsg: "not configured"},
		{name: "store cannot be opened", store: errStore{err: boom}, want: backend.HealthStatusError, wantMsg: "dial tcp 127.0.0.1:1"},
		{name: "store reachable", store: newMemoryStore(), want: backend.HealthStatusOk, wantMsg: "ok"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, err := newApp(context.Background(), backend.AppInstanceSettings{}, tc.store, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			got, err := app.CheckHealth(context.Background(), &backend.CheckHealthRequest{})
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != tc.want {
				t.Fatalf("status=%v want %v (%s)", got.Status, tc.want, got.Message)
			}
			if !strings.Contains(got.Message, tc.wantMsg) {
				t.Fatalf("message=%q want it to contain %q", got.Message, tc.wantMsg)
			}
		})
	}
}

// TestDatasourceCheckHealth: unlike the app, the datasource's queries cannot work
// without a store, so an absent one is an error rather than a degraded ok.
func TestDatasourceCheckHealth(t *testing.T) {
	clearStoreEnv(t)
	boom := errors.New("forecast store: dial tcp 127.0.0.1:1: connect: connection refused")
	for _, tc := range []struct {
		name    string
		store   SnapshotStore
		want    backend.HealthStatus
		wantMsg string
	}{
		{name: "no store configured", store: nil, want: backend.HealthStatusError, wantMsg: msgStoreOff},
		{name: "store cannot be opened", store: errStore{err: boom}, want: backend.HealthStatusError, wantMsg: "dial tcp 127.0.0.1:1"},
		{name: "store reachable", store: newMemoryStore(), want: backend.HealthStatusOk, wantMsg: "ok"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ds, err := newDatasource(context.Background(), backend.DataSourceInstanceSettings{}, tc.store)
			if err != nil {
				t.Fatal(err)
			}
			got, err := ds.CheckHealth(context.Background(), &backend.CheckHealthRequest{
				PluginContext: backend.PluginContext{OrgID: 1},
			})
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != tc.want {
				t.Fatalf("status=%v want %v (%s)", got.Status, tc.want, got.Message)
			}
			if !strings.Contains(got.Message, tc.wantMsg) {
				t.Fatalf("message=%q want it to contain %q", got.Message, tc.wantMsg)
			}
		})
	}
}
