package handler

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// TVConfig holds every threshold and probe of the TV alarm layer
// (RUSL-1786) in one place, read from the MULTICA_TV_CONFIG JSON at start,
// so a threshold changes with a restart, not a rebuild. Secrets never sit in
// the JSON: a probe names the environment variable that holds its token.
type TVConfig struct {
	// Runs the host can really carry at once; Supply is running / this.
	HostCeiling int
	// No run going while the oldest queued task waits longer than this.
	StallAfter time.Duration
	// This many failed runs inside FailedBurstWindow raise failed_burst.
	FailedBurst       int
	FailedBurstWindow time.Duration
	// Two active runs on one issue are a duplicate only once they have run
	// together this long; a hand-off between agents overlaps for seconds.
	DuplicateRunAfter time.Duration
	// OOM-killed runs inside this window keep the oom signal raised.
	OOMWindow time.Duration
	// Failed autopilot runs inside this window raise autopilot.
	AutopilotWindow time.Duration
	// The active sprint ends within MinimumsHours with less than
	// MinimumsPercent of its issues done.
	MinimumsHours   int
	MinimumsPercent int
	// HTTP probes by signal key: llm, site, budget, llm_slow. A probe is
	// healthy on 2xx; whatever reports budget or model speed only has to
	// answer non-2xx when the limit is crossed.
	Probes map[string]TVProbe
	// Daemon ids whose outage is a warning: the servers, not laptops.
	WatchedDaemons []string
	// Directory with the alarm sound files, from MULTICA_TV_SOUNDS_DIR.
	SoundsDir string
}

type TVProbe struct {
	URL    string
	Bearer string
}

// tvProbeKeys are the keys a probe may be configured for.
var tvProbeKeys = []string{tvHealthKeyLLM, tvHealthKeySite, tvHealthKeyBudget, tvHealthKeyLLMSlow}

func DefaultTVConfig() TVConfig {
	return TVConfig{
		HostCeiling:       12,
		StallAfter:        10 * time.Minute,
		FailedBurst:       5,
		FailedBurstWindow: 10 * time.Minute,
		DuplicateRunAfter: 2 * time.Minute,
		OOMWindow:         15 * time.Minute,
		AutopilotWindow:   30 * time.Minute,
		MinimumsHours:     24,
		MinimumsPercent:   70,
		Probes:            map[string]TVProbe{},
	}
}

type tvConfigJSON struct {
	HostCeiling              *int                         `json:"host_ceiling"`
	StallMinutes             *int                         `json:"stall_minutes"`
	FailedBurst              *int                         `json:"failed_burst"`
	FailedBurstWindowMinutes *int                         `json:"failed_burst_window_minutes"`
	DuplicateRunMinutes      *int                         `json:"duplicate_run_minutes"`
	OOMWindowMinutes         *int                         `json:"oom_window_minutes"`
	AutopilotWindowMinutes   *int                         `json:"autopilot_window_minutes"`
	MinimumsHours            *int                         `json:"minimums_hours"`
	MinimumsPercent          *int                         `json:"minimums_percent"`
	Probes                   map[string]tvProbeConfigJSON `json:"probes"`
	WatchedDaemons           []string                     `json:"watched_daemons"`
}

type tvProbeConfigJSON struct {
	URL       string `json:"url"`
	BearerEnv string `json:"bearer_env"`
}

// ParseTVConfig reads MULTICA_TV_CONFIG. Empty input gives the defaults;
// `getenv` resolves a probe's bearer_env.
func ParseTVConfig(raw string, getenv func(string) string) (TVConfig, error) {
	cfg := DefaultTVConfig()
	if strings.TrimSpace(raw) == "" {
		return cfg, nil
	}
	var in tvConfigJSON
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return cfg, fmt.Errorf("MULTICA_TV_CONFIG: %w", err)
	}
	positive := func(name string, v *int, set func(int)) error {
		if v == nil {
			return nil
		}
		if *v <= 0 {
			return fmt.Errorf("MULTICA_TV_CONFIG: %s must be positive", name)
		}
		set(*v)
		return nil
	}
	minutes := func(dst *time.Duration) func(int) {
		return func(v int) { *dst = time.Duration(v) * time.Minute }
	}
	for _, step := range []error{
		positive("host_ceiling", in.HostCeiling, func(v int) { cfg.HostCeiling = v }),
		positive("stall_minutes", in.StallMinutes, minutes(&cfg.StallAfter)),
		positive("failed_burst", in.FailedBurst, func(v int) { cfg.FailedBurst = v }),
		positive("failed_burst_window_minutes", in.FailedBurstWindowMinutes, minutes(&cfg.FailedBurstWindow)),
		positive("duplicate_run_minutes", in.DuplicateRunMinutes, minutes(&cfg.DuplicateRunAfter)),
		positive("oom_window_minutes", in.OOMWindowMinutes, minutes(&cfg.OOMWindow)),
		positive("autopilot_window_minutes", in.AutopilotWindowMinutes, minutes(&cfg.AutopilotWindow)),
		positive("minimums_hours", in.MinimumsHours, func(v int) { cfg.MinimumsHours = v }),
		positive("minimums_percent", in.MinimumsPercent, func(v int) { cfg.MinimumsPercent = v }),
	} {
		if step != nil {
			return DefaultTVConfig(), step
		}
	}
	if cfg.MinimumsPercent > 100 {
		return DefaultTVConfig(), fmt.Errorf("MULTICA_TV_CONFIG: minimums_percent must be at most 100")
	}
	for _, id := range in.WatchedDaemons {
		if id = strings.TrimSpace(id); id != "" {
			cfg.WatchedDaemons = append(cfg.WatchedDaemons, id)
		}
	}
	for key, probe := range in.Probes {
		known := false
		for _, k := range tvProbeKeys {
			known = known || k == key
		}
		if !known {
			return DefaultTVConfig(), fmt.Errorf("MULTICA_TV_CONFIG: unknown probe %q", key)
		}
		url := strings.TrimSpace(probe.URL)
		if url == "" {
			continue
		}
		p := TVProbe{URL: url}
		if probe.BearerEnv != "" {
			p.Bearer = strings.TrimSpace(getenv(probe.BearerEnv))
		}
		cfg.Probes[key] = p
	}
	return cfg, nil
}
