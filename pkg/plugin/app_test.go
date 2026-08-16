package plugin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

func TestCheckHealthAppliesJSONData(t *testing.T) {
	t.Parallel()
	inst, err := NewApp(context.Background(), backend.AppInstanceSettings{})
	if err != nil {
		t.Fatal(err)
	}
	app := inst.(*App)
	defer app.Dispose()

	raw, err := json.Marshal(map[string]any{"aheadMinutes": 60})
	if err != nil {
		t.Fatal(err)
	}
	res, err := app.CheckHealth(context.Background(), &backend.CheckHealthRequest{
		PluginContext: backend.PluginContext{
			AppInstanceSettings: &backend.AppInstanceSettings{JSONData: raw},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != backend.HealthStatusOk {
		t.Fatalf("status %v message %s", res.Status, res.Message)
	}
	if app.cfg.AheadMinutes != 60 {
		t.Fatalf("aheadMinutes %d want 60 (saved jsonData must apply on health)", app.cfg.AheadMinutes)
	}
	if !strings.Contains(res.Message, "publisher off") {
		t.Fatalf("message %q", res.Message)
	}
}
