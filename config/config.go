package config

import (
	"log"
	"os"
	"strconv"
	"text/template"
	"time"
)

type Config struct {
	Template *template.Template
	Poll     PollConfig
}

// PollConfig holds the feed-polling behaviour. There is intentionally no lower
// bound on PollInterval: polling as often as every minute is a supported
// use-case. Server hints (max-age, Retry-After, ttl) can only slow polling down.
type PollConfig struct {
	Interval    time.Duration // base poll interval (POLL_INTERVAL)
	IntervalMax time.Duration // cap for adaptive slow-down (POLL_INTERVAL_MAX)
	Adaptive    bool          // FRB023: slow feeds that rarely update (POLL_ADAPTIVE)
	Concurrency int           // max simultaneous fetches (POLL_CONCURRENCY)
	Tick        time.Duration // scheduler granularity (POLL_TICK)
}

func GetPollConfig() PollConfig {
	cfg := PollConfig{
		Interval:    durationEnv("POLL_INTERVAL", 10*time.Minute),
		IntervalMax: durationEnv("POLL_INTERVAL_MAX", 6*time.Hour),
		Adaptive:    boolEnv("POLL_ADAPTIVE", true),
		Concurrency: intEnv("POLL_CONCURRENCY", 8),
		Tick:        durationEnv("POLL_TICK", 30*time.Second),
	}
	if cfg.IntervalMax < cfg.Interval {
		log.Printf("POLL_INTERVAL_MAX (%s) is below POLL_INTERVAL (%s), using %s", cfg.IntervalMax, cfg.Interval, cfg.Interval)
		cfg.IntervalMax = cfg.Interval
	}
	return cfg
}

func durationEnv(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return d
	}
	log.Printf("Invalid %s=%q, using default %s", key, v, fallback)
	return fallback
}

func intEnv(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return n
	}
	log.Printf("Invalid %s=%q, using default %d", key, v, fallback)
	return fallback
}

func boolEnv(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	if b, err := strconv.ParseBool(v); err == nil {
		return b
	}
	log.Printf("Invalid %s=%q, using default %t", key, v, fallback)
	return fallback
}

func fileExists(fileName string) bool {
	if _, err := os.Stat(fileName); err == nil {
		return true
	}
	return false
}

func GetTemplate(path string) (*template.Template, error) {
	if fileExists(path) {
		return template.ParseFiles(path)
	} else {
		return template.New("post").Parse(`<b>{{.Title}}</b>
<i>{{.FeedTitle}}</i>
{{- if ne .Content "" }}
{{.Content}}
{{- end }}
<a href="{{.PostLink}}">Weiterlesen auf {{.PostDomain}}</a>`)
	}
}
