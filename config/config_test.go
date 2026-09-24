package config

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGetPollConfigDefaults(t *testing.T) {
	for _, key := range []string{"POLL_INTERVAL", "POLL_INTERVAL_MAX", "POLL_ADAPTIVE", "POLL_CONCURRENCY", "POLL_TICK"} {
		t.Setenv(key, "")
	}

	want := PollConfig{
		Interval:    10 * time.Minute,
		IntervalMax: 6 * time.Hour,
		Adaptive:    true,
		Concurrency: 8,
		Tick:        30 * time.Second,
	}
	if got := GetPollConfig(); got != want {
		t.Errorf("GetPollConfig() = %+v, want %+v", got, want)
	}
}

func TestGetPollConfigFromEnv(t *testing.T) {
	t.Setenv("POLL_INTERVAL", "1m")
	t.Setenv("POLL_INTERVAL_MAX", "2h")
	t.Setenv("POLL_ADAPTIVE", "false")
	t.Setenv("POLL_CONCURRENCY", "3")
	t.Setenv("POLL_TICK", "5s")

	want := PollConfig{
		Interval:    time.Minute,
		IntervalMax: 2 * time.Hour,
		Adaptive:    false,
		Concurrency: 3,
		Tick:        5 * time.Second,
	}
	if got := GetPollConfig(); got != want {
		t.Errorf("GetPollConfig() = %+v, want %+v", got, want)
	}
}

func TestGetPollConfigInvalidValuesFallBack(t *testing.T) {
	t.Setenv("POLL_INTERVAL", "10m # comment")
	t.Setenv("POLL_INTERVAL_MAX", "-1h")
	t.Setenv("POLL_ADAPTIVE", "maybe")
	t.Setenv("POLL_CONCURRENCY", "0")
	t.Setenv("POLL_TICK", "fast")

	got := GetPollConfig()
	if got.Interval != 10*time.Minute || got.IntervalMax != 6*time.Hour || !got.Adaptive ||
		got.Concurrency != 8 || got.Tick != 30*time.Second {
		t.Errorf("invalid values did not fall back to defaults: %+v", got)
	}
}

func TestGetPollConfigRaisesMaxToInterval(t *testing.T) {
	t.Setenv("POLL_INTERVAL", "12h")
	t.Setenv("POLL_INTERVAL_MAX", "1h")

	if got := GetPollConfig(); got.IntervalMax != 12*time.Hour {
		t.Errorf("IntervalMax = %s, want 12h", got.IntervalMax)
	}
}

func TestGetTemplateDefault(t *testing.T) {
	tmpl, err := GetTemplate("does-not-exist.gohtml")
	if err != nil {
		t.Fatal(err)
	}
	if tmpl.Name() != "post" {
		t.Errorf("template name = %q, want post", tmpl.Name())
	}
}

func TestLoadTemplateFromEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "custom.gohtml")
	if err := os.WriteFile(path, []byte("[#RSS] {{.Title}}"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POST_TEMPLATE", path)

	tmpl, err := LoadTemplate()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, struct{ Title string }{"Hallo"}); err != nil {
		t.Fatal(err)
	}
	if out.String() != "[#RSS] Hallo" {
		t.Errorf("rendered %q", out.String())
	}
}

func TestLoadTemplateMissingFileFails(t *testing.T) {
	t.Setenv("POST_TEMPLATE", filepath.Join(t.TempDir(), "missing.gohtml"))
	if _, err := LoadTemplate(); err == nil {
		t.Error("an explicitly configured but missing template must be an error")
	}
}

func TestLoadTemplateDefault(t *testing.T) {
	t.Setenv("POST_TEMPLATE", "")
	t.Chdir(t.TempDir())

	tmpl, err := LoadTemplate()
	if err != nil {
		t.Fatal(err)
	}
	if tmpl.Name() != "post" {
		t.Errorf("template name = %q, want built-in post", tmpl.Name())
	}
}
