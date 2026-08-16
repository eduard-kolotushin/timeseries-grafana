package plugin

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestParseConfigDefaults(t *testing.T) {
	t.Parallel()
	cfg, err := parseConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Enabled || cfg.Lookback != defaultLookback || cfg.AheadMinutes != 1 || cfg.Interval != time.Minute {
		t.Fatalf("defaults: %+v", cfg)
	}
}

func TestParseConfigJSON(t *testing.T) {
	t.Parallel()
	raw, _ := json.Marshal(map[string]any{
		"enabled":         true,
		"druidBroker":     "http://druid-broker:8082",
		"druidDatasource": "metrics",
		"kafkaBrokers":    "kafka:9092, kafka:9093",
		"kafkaTopic":      "baselines",
		"lookback":        "48h",
		"aheadMinutes":    "3",
		"interval":        "30s",
		"calendar":        "ru",
	})
	cfg, err := parseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled || cfg.DruidBroker != "http://druid-broker:8082" || cfg.DruidDatasource != "metrics" {
		t.Fatalf("core: %+v", cfg)
	}
	if len(cfg.KafkaBrokers) != 2 || cfg.KafkaBrokers[0] != "kafka:9092" || cfg.KafkaTopic != "baselines" {
		t.Fatalf("kafka: %+v", cfg.KafkaBrokers)
	}
	if cfg.Lookback != 48*time.Hour || cfg.AheadMinutes != 3 || cfg.Interval != 30*time.Second || cfg.Calendar != "ru" {
		t.Fatalf("timing: %+v", cfg)
	}
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
}

func TestParseConfigEnablesWhenFullyConfigured(t *testing.T) {
	t.Parallel()
	raw, _ := json.Marshal(map[string]any{
		"druidBroker":     "http://druid-broker:8082",
		"druidDatasource": "metrics",
		"kafkaBrokers":    "kafka:9092",
		"kafkaTopic":      "baselines",
	})
	cfg, err := parseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled {
		t.Fatal("expected enabled when broker, datasource, and kafka are set")
	}
}

func TestPublisherConfigEqual(t *testing.T) {
	t.Parallel()
	base := PublisherConfig{
		Enabled:         true,
		DruidBroker:     "http://druid-broker:8082",
		DruidDatasource: "metrics",
		KafkaBrokers:    []string{"kafka:9092"},
		KafkaTopic:      "baselines",
		Lookback:        defaultLookback,
		AheadMinutes:    1,
		Interval:        defaultInterval,
	}
	for _, tc := range []struct {
		name string
		mut  func(*PublisherConfig)
		want bool
	}{
		{"same", func(*PublisherConfig) {}, true},
		{"aheadMinutes", func(c *PublisherConfig) { c.AheadMinutes = 60 }, false},
		{"interval", func(c *PublisherConfig) { c.Interval = 30 * time.Second }, false},
		{"brokers", func(c *PublisherConfig) { c.KafkaBrokers = []string{"kafka:9093"} }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			other := base
			other.KafkaBrokers = append([]string(nil), base.KafkaBrokers...)
			tc.mut(&other)
			if got := base.equal(other); got != tc.want {
				t.Fatalf("equal=%v want %v", got, tc.want)
			}
		})
	}
}

func TestPublisherConfigValidate(t *testing.T) {
	t.Parallel()
	ok := PublisherConfig{
		DruidBroker:     "http://druid-broker:8082",
		DruidDatasource: "metrics",
		KafkaBrokers:    []string{"kafka:9092"},
		KafkaTopic:      "baselines",
		Lookback:        time.Hour,
		AheadMinutes:    1,
		Interval:        time.Minute,
	}
	for _, tc := range []struct {
		name string
		mut  func(*PublisherConfig)
		want string
	}{
		{"ok", func(*PublisherConfig) {}, ""},
		{"broker", func(c *PublisherConfig) { c.DruidBroker = "druid-broker:8082" }, "absolute URL"},
		{"datasource", func(c *PublisherConfig) { c.DruidDatasource = "metrics-1;drop" }, "druidDatasource"},
		{"kafka", func(c *PublisherConfig) { c.KafkaBrokers = nil }, "kafkaBrokers"},
		{"topic", func(c *PublisherConfig) { c.KafkaTopic = "" }, "kafkaTopic"},
		{"lookback", func(c *PublisherConfig) { c.Lookback = time.Second }, "lookback"},
		{"ahead", func(c *PublisherConfig) { c.AheadMinutes = 0 }, "aheadMinutes"},
		{"interval", func(c *PublisherConfig) { c.Interval = time.Millisecond }, "interval"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := ok
			tc.mut(&c)
			err := c.validate()
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v want substring %q", err, tc.want)
			}
		})
	}
}
