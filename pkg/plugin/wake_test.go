package plugin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestGrafanaHealthURL(t *testing.T) {
	t.Setenv("GF_SERVER_HTTP_PORT", "3000")
	if got := grafanaHealthURL(); got != "http://127.0.0.1:3000"+pluginHealthPath {
		t.Fatalf("default URL %s", got)
	}
	t.Setenv("GF_SERVER_HTTP_PORT", "3001")
	if got := grafanaHealthURL(); got != "http://127.0.0.1:3001"+pluginHealthPath {
		t.Fatalf("port URL %s", got)
	}
}

func TestWakeLoop(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		status []int
		wantN  int32
	}{
		{"ok first try", []int{http.StatusOK}, 1},
		{"retry then ok", []int{http.StatusBadGateway, http.StatusOK}, 2},
		{"unauthorized stops", []int{http.StatusUnauthorized}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var n atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				i := int(n.Add(1) - 1)
				code := tc.status[len(tc.status)-1]
				if i < len(tc.status) {
					code = tc.status[i]
				}
				w.WriteHeader(code)
			}))
			t.Cleanup(srv.Close)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			wakeLoop(ctx, srv.Client(), srv.URL, time.Millisecond, 5)
			if n.Load() != tc.wantN {
				t.Fatalf("calls %d want %d", n.Load(), tc.wantN)
			}
		})
	}
}
