package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

	// maxForecastPoints bounds one emitted window. It is the twin of the library's
	// forecast.MaxForecastPoints (whose error httpStatusFor maps to the same 413),
	// kept here as the plugin's own constant so the request is refused before the
	// fit: without it a single request whose `to` is far enough makes ForecastRange
	// allocate a slice of billions of points and the process dies with no error
	// surfaced. See checkForecastWindow.
	maxForecastPoints = 1_000_000

	// maxJSONBytesPerPoint is the per-element body budget the pre-flight in
	// handleForecast uses to refuse an impossible request before it is decoded: a
	// body carries two arrays (times, values) of at most maxTrainPoints elements
	// each, and one JSON float costs ~24 characters at worst (-1.7976931348623157e+308
	// plus its separator), so 32 bytes per element bounds a legal body with headroom.
	maxJSONBytesPerPoint = 32

	// maxTrainSourceBytes is the part of a fit request that carries no training points: the
	// `trainSource` replay payload (the panel's own metric targets and identity, which the
	// scheduler stores and POSTs back) plus the model envelope around it. The pre-flight used to
	// budget the two point arrays alone, so a legal 100,000-point fit whose target set pushed the
	// body past that budget was refused with "…for a legal training series" — a message that was
	// false. checkTrainSourceLen enforces this bound after the decode, which is what keeps the
	// pre-flight's claim true; a realistic panel serialises its targets into a few kilobytes.
	maxTrainSourceBytes = 1 << 20

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
	// errTrainBodyTooLarge is the pre-flight refusal: the body is larger than any
	// legal training request could be, so it is refused before it is decoded.
	errTrainBodyTooLarge = errors.New("forecast: request body too large for a legal training series")
	// errTrainSourceTooLarge is the replay-payload cap. The body can be legal-sized and still
	// carry query objects past what the fit path stores, so it has its own 413 reason rather
	// than sharing the pre-flight's "a legal training series cannot be this large" one. The
	// message is derived from maxTrainSourceBytes, so the number it reports cannot drift from
	// the number it enforces.
	errTrainSourceTooLarge = fmt.Errorf("forecast: trainSource is larger than %d bytes", maxTrainSourceBytes)
	// errWindowTooManyPoints is the emitted-window cap, checked before any
	// allocation on the fit, restore and datasource paths. Its message matches the
	// library's forecast.ErrTooManyPoints, and httpStatusFor maps the two to the
	// same 413, so a caller cannot tell which side refused the window.
	errWindowTooManyPoints = errors.New("forecast: window has too many points")

	maxTrainPoints       = defaultMaxTrainPoints
	maxForecastBodyBytes = int64(defaultMaxForecastBody)
	maxQueryJSONBytes    = defaultMaxQueryJSONBytes
)

// maxTrainBodyBytes is the largest request body that can describe a training
// series: two arrays of at most maxTrainPoints elements each, budgeted at
// maxJSONBytesPerPoint bytes per element, plus the replay payload and envelope
// maxTrainSourceBytes bounds. A body above it cannot be legal, and decoding it
// first would be the expensive mistake — 8 bytes of []int64/[]float64 per element
// plus the decoder's slice growth — so handleForecast refuses it by Content-Length
// before json.Decoder ever sees it. checkTrainSourceLen is the other half of the
// claim: without it the allowance would be a guess rather than a limit.
func maxTrainBodyBytes() int64 {
	return 2*int64(maxTrainPoints)*maxJSONBytesPerPoint + maxTrainSourceBytes
}

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

// checkTrainSourceLen bounds the replay payload a fit may carry (maxTrainSourceBytes). It measures
// the re-marshalled trainSource rather than one field, so a long identity string costs the same as a
// long query list, and it is the fit path's whole-payload twin of the datasource's per-query
// maxQueryJSONBytes cap. A nil source (a probe, or a fit the overlay sent without one) costs nothing.
func checkTrainSourceLen(source *TrainSource) error {
	if source == nil {
		return nil
	}
	raw, err := json.Marshal(source)
	if err != nil {
		return err
	}
	if int64(len(raw)) > maxTrainSourceBytes {
		return errTrainSourceTooLarge
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
