package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

const (
	defaultMaxTrainPoints    = 100_000
	defaultMaxInflight       = 4
	defaultMaxForecastBody   = 16 << 20
	defaultMaxQueryJSONBytes = 64 << 10

	// Receive headroom over the app's body cap. The SDK's own default receive limit is the
	// cap's twin, and a body at the cap then fails the transport (500
	// plugin.requestFailureError) before the handler's MaxBytesReader can answer 413. A
	// whole cap of headroom keeps the plugin's own limit the one a caller meets; only a
	// message above cap+headroom is refused by the transport itself.
	grpcReceiveHeadroom = defaultMaxForecastBody
)

var (
	errBusy         = errors.New("forecast: busy")
	errTrainTooLong = errors.New("forecast: training series exceeds 100000 points")
	errBodyTooLarge = errors.New("forecast: request body too large")

	maxTrainPoints       = defaultMaxTrainPoints
	maxForecastBodyBytes = int64(defaultMaxForecastBody)
	maxQueryJSONBytes    = defaultMaxQueryJSONBytes
)

// GRPCSettings is the gRPC server configuration both plugin processes serve with: the body
// cap plus headroom, so an oversize body reaches the handler's MaxBytesReader and is
// answered with 413 instead of failing the transport.
func GRPCSettings() backend.GRPCSettings {
	return backend.GRPCSettings{MaxReceiveMsgSize: int(maxForecastBodyBytes) + grpcReceiveHeadroom}
}

type workLimiter struct {
	ch chan struct{}
}

func newWorkLimiter(n int) *workLimiter {
	if n < 1 {
		n = defaultMaxInflight
	}
	return &workLimiter{ch: make(chan struct{}, n)}
}

func (l *workLimiter) try(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if l == nil {
		return func() {}, nil
	}
	select {
	case l.ch <- struct{}{}:
		return func() { <-l.ch }, nil
	default:
		return nil, errBusy
	}
}

func runLimited[T any](ctx context.Context, lim *workLimiter, fn func() (T, error)) (T, error) {
	var zero T
	release, err := lim.try(ctx)
	if err != nil {
		return zero, err
	}
	defer release()
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	return fn()
}

func parseMaxInflight(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return defaultMaxInflight
	}
	return n
}

func maxInflightFrom(ctx context.Context, jsonData []byte) int {
	jd := map[string]any{}
	if len(jsonData) > 0 {
		_ = json.Unmarshal(jsonData, &jd)
	}
	look := storeLookup{
		getenv: os.Getenv,
		cfg:    backend.GrafanaConfigFromContext(ctx),
		json:   jd,
	}
	if n := parseMaxInflight(look.get("FORECAST_MAX_INFLIGHT", "MAX_INFLIGHT", "max_inflight", "maxInflight")); n >= 1 {
		return n
	}
	return defaultMaxInflight
}

func checkTrainLen(times, values int) error {
	if times > maxTrainPoints || values > maxTrainPoints {
		return errTrainTooLong
	}
	return nil
}

func dataStatusFor(err error) backend.Status {
	switch httpStatusFor(err) {
	case 429:
		return backend.StatusTooManyRequests
	case 413:
		return backend.Status(413)
	case 400:
		return backend.StatusBadRequest
	case 408:
		return backend.StatusTimeout
	default:
		return backend.StatusInternal
	}
}
