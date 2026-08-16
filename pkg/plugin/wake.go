package plugin

import (
	"context"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend/log"
)

const (
	pluginHealthPath = "/api/plugins/eduardkolotushin-forecast-app/health"
	wakeAttempts     = 90
	wakePause        = 2 * time.Second
	wakeHTTPTimeout  = 2 * time.Second
)

// StartWake asks Grafana to CheckHealth this plugin once the HTTP API is up.
// Grafana starts the backend process at boot but does not create an app instance
// until an RPC; this is that RPC, so the ticker runs on any Grafana install.
func StartWake(ctx context.Context) {
	go wakeLoop(ctx, &http.Client{Timeout: wakeHTTPTimeout}, grafanaHealthURL(), wakePause, wakeAttempts)
}

func grafanaHealthURL() string {
	port := os.Getenv("GF_SERVER_HTTP_PORT")
	if port == "" {
		port = "3000"
	}
	return "http://127.0.0.1:" + port + pluginHealthPath
}

func wakeLoop(ctx context.Context, client *http.Client, healthURL string, pause time.Duration, attempts int) {
	if client == nil || healthURL == "" || attempts < 1 {
		return
	}
	for i := 0; i < attempts; i++ {
		if err := ctx.Err(); err != nil {
			return
		}
		ok, retry := wakeOnce(ctx, client, healthURL)
		if ok {
			log.DefaultLogger.Info("baseline publisher wake ok")
			return
		}
		if !retry {
			log.DefaultLogger.Info("baseline publisher wake skipped (Grafana requires login for /health); ticker starts on UI load or Configuration save")
			return
		}
		timer := time.NewTimer(pause)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
	log.DefaultLogger.Warn("baseline publisher wake timed out; ticker starts on UI load or Configuration save")
}

func wakeOnce(ctx context.Context, client *http.Client, healthURL string) (ok, retry bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
	if err != nil {
		return false, false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, true
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	switch resp.StatusCode {
	case http.StatusOK:
		return true, false
	case http.StatusUnauthorized, http.StatusForbidden:
		return false, false
	default:
		return false, true
	}
}
