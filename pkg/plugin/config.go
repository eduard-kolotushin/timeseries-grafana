package plugin

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	defaultLookback     = 336 * time.Hour
	defaultAheadMinutes = 1
	defaultInterval     = time.Minute
	maxAheadMinutes     = 10080
)

var datasourceName = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// PublisherConfig is app jsonData for the Druid → Kafka baseline loop.
type PublisherConfig struct {
	Enabled         bool
	DruidBroker     string
	DruidDatasource string
	KafkaBrokers    []string
	KafkaTopic      string
	Lookback        time.Duration
	AheadMinutes    int
	Interval        time.Duration
	Calendar        string
}

func parseConfig(raw json.RawMessage) (PublisherConfig, error) {
	cfg := PublisherConfig{
		Lookback:     defaultLookback,
		AheadMinutes: defaultAheadMinutes,
		Interval:     defaultInterval,
	}
	if len(raw) == 0 || string(raw) == "null" {
		return cfg, nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return PublisherConfig{}, fmt.Errorf("plugin jsonData: %w", err)
	}
	cfg.DruidBroker = strings.TrimSpace(asString(m["druidBroker"]))
	cfg.DruidDatasource = strings.TrimSpace(asString(m["druidDatasource"]))
	cfg.KafkaBrokers = splitBrokers(asString(m["kafkaBrokers"]))
	cfg.KafkaTopic = strings.TrimSpace(asString(m["kafkaTopic"]))
	cfg.Calendar = strings.TrimSpace(asString(m["calendar"]))
	if _, ok := m["enabled"]; ok {
		cfg.Enabled = asBool(m["enabled"], false)
	} else {
		cfg.Enabled = cfg.DruidBroker != "" && cfg.DruidDatasource != "" && len(cfg.KafkaBrokers) > 0 && cfg.KafkaTopic != ""
	}
	if v, ok := m["lookback"]; ok && asString(v) != "" {
		d, err := time.ParseDuration(asString(v))
		if err != nil {
			return PublisherConfig{}, fmt.Errorf("lookback: %w", err)
		}
		cfg.Lookback = d
	}
	if v, ok := m["interval"]; ok && asString(v) != "" {
		d, err := time.ParseDuration(asString(v))
		if err != nil {
			return PublisherConfig{}, fmt.Errorf("interval: %w", err)
		}
		cfg.Interval = d
	}
	if _, ok := m["aheadMinutes"]; ok {
		cfg.AheadMinutes = asInt(m["aheadMinutes"], defaultAheadMinutes)
	}
	return cfg, nil
}

func (c PublisherConfig) equal(o PublisherConfig) bool {
	return c.Enabled == o.Enabled &&
		c.DruidBroker == o.DruidBroker &&
		c.DruidDatasource == o.DruidDatasource &&
		c.KafkaTopic == o.KafkaTopic &&
		c.Lookback == o.Lookback &&
		c.AheadMinutes == o.AheadMinutes &&
		c.Interval == o.Interval &&
		c.Calendar == o.Calendar &&
		slices.Equal(c.KafkaBrokers, o.KafkaBrokers)
}

func (c PublisherConfig) validate() error {
	if strings.TrimSpace(c.DruidBroker) == "" {
		return fmt.Errorf("druidBroker is required")
	}
	u, err := url.Parse(c.DruidBroker)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("druidBroker must be an absolute URL")
	}
	if !datasourceName.MatchString(c.DruidDatasource) {
		return fmt.Errorf("druidDatasource must be [A-Za-z0-9_]+")
	}
	if len(c.KafkaBrokers) == 0 {
		return fmt.Errorf("kafkaBrokers is required")
	}
	if c.KafkaTopic == "" {
		return fmt.Errorf("kafkaTopic is required")
	}
	if c.Lookback < time.Minute {
		return fmt.Errorf("lookback must be at least 1m")
	}
	if c.AheadMinutes < 1 || c.AheadMinutes > maxAheadMinutes {
		return fmt.Errorf("aheadMinutes must be in 1..%d", maxAheadMinutes)
	}
	if c.Interval < time.Second {
		return fmt.Errorf("interval must be at least 1s")
	}
	return nil
}

func splitBrokers(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func asString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case fmt.Stringer:
		return x.String()
	default:
		return fmt.Sprint(x)
	}
}

func asBool(v any, def bool) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		b, err := strconv.ParseBool(strings.TrimSpace(x))
		if err != nil {
			return def
		}
		return b
	default:
		return def
	}
}

func asInt(v any, def int) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case json.Number:
		i, err := x.Int64()
		if err != nil {
			return def
		}
		return int(i)
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(x))
		if err != nil {
			return def
		}
		return i
	default:
		return def
	}
}
