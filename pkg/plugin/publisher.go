package plugin

import (
	"context"
	"math"
	"time"

	"github.com/eduard-kolotushin/timeseries"
	forecast "github.com/eduard-kolotushin/timeseries-forecast"
	"github.com/grafana/grafana-plugin-sdk-go/backend/log"
)

type publisher struct {
	cfg       PublisherConfig
	src       metricReader
	sink      baselineSink
	cal       *forecast.Calendar
	published map[string]int64
}

func newPublisher(cfg PublisherConfig, src metricReader, sink baselineSink, cal *forecast.Calendar) *publisher {
	return &publisher{
		cfg:       cfg,
		src:       src,
		sink:      sink,
		cal:       cal,
		published: make(map[string]int64),
	}
}

func (p *publisher) run(ctx context.Context) {
	t := time.NewTicker(p.cfg.Interval)
	defer t.Stop()
	p.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.tick(ctx)
		}
	}
}

func (p *publisher) tick(ctx context.Context) {
	if err := ctx.Err(); err != nil {
		return
	}
	spans, err := p.src.Hashes(ctx)
	if err != nil {
		log.DefaultLogger.Error("baseline publisher: list hashes", "err", err)
		return
	}
	for _, span := range spans {
		if err := ctx.Err(); err != nil {
			return
		}
		if err := p.publishHash(ctx, span); err != nil {
			log.DefaultLogger.Error("baseline publisher: metric", "metric_hash", span.Hash, "err", err)
		}
	}
}

func (p *publisher) publishHash(ctx context.Context, span metricSpan) error {
	if span.Max.Sub(span.Min) < p.cfg.Lookback {
		return nil
	}
	from := span.Max.Add(-p.cfg.Lookback)
	pts, err := p.src.Series(ctx, span.Hash, from)
	if err != nil {
		return err
	}
	if len(pts) < 2 {
		return nil
	}
	step := pts[len(pts)-1].Time.Sub(pts[len(pts)-2].Time)
	if step != time.Minute {
		return nil
	}
	times := make([]time.Time, len(pts))
	values := make([]float64, len(pts))
	for i, pt := range pts {
		times[i] = pt.Time
		values[i] = pt.Value
	}
	s, err := timeseries.New(times, values)
	if err != nil {
		return err
	}
	fitted, err := forecast.FitSeasonalBaseline(s, forecast.SeasonMinuteOfWeek, p.cal)
	if err != nil {
		return err
	}
	out, err := fitted.Forecast(p.cfg.AheadMinutes)
	if err != nil {
		return err
	}
	if out.Len() == 0 {
		return nil
	}
	i := out.Len() - 1
	ts := out.Times()[i]
	v := out.Values()[i]
	if math.IsNaN(v) {
		return nil
	}
	ms := ts.UTC().UnixMilli()
	if prev, ok := p.published[span.Hash]; ok && ms <= prev {
		return nil
	}
	msg := BaselineMessage{
		MetricHash:    span.Hash,
		MetricTS:      ms,
		BaselineValue: v,
	}
	if err := p.sink.Publish(ctx, msg); err != nil {
		return err
	}
	p.published[span.Hash] = ms
	return nil
}
