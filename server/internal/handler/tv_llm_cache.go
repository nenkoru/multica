package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// The board's prompt-cache block (RUSL-1860): how much of the local model's
// prompt came from its prefix cache, beside what the same requests would have
// cost in the cloud. The numbers are computed elsewhere (cache_stats.py serve
// in the LiteLLM namespace); the server only forwards the few fields the
// screen draws, so key aliases and per-model detail never reach a kiosk.

const (
	tvLLMCacheTTL = time.Minute
	// A source that stops answering keeps its last summary on screen this
	// long, then the block hides rather than show old numbers as current.
	tvLLMCacheStale = 30 * time.Minute
	// The goal for the reusable-token measure, drawn as the target line.
	tvLLMCacheTarget  = 99.5
	tvLLMCacheMaxBody = 1 << 20
)

type TVLLMCacheHour struct {
	At          string   `json:"at"`
	Requests    int64    `json:"requests"`
	HitReusable *float64 `json:"hit_reusable"`
	HitAll      *float64 `json:"hit_all"`
}

type TVLLMCache struct {
	Hours       int              `json:"hours"`
	Requests    int64            `json:"requests"`
	HitReusable *float64         `json:"hit_reusable"`
	HitAll      *float64         `json:"hit_all"`
	Target      float64          `json:"target"`
	CloudRub    *int64           `json:"cloud_rub"`
	SpentRub    *int64           `json:"spent_rub"`
	SavedRub    *int64           `json:"saved_rub"`
	Hourly      []TVLLMCacheHour `json:"hourly"`
	ComputedAt  string           `json:"computed_at"`
}

// tvLLMCacheSource is the report JSON of cache_stats.py; unknown fields are
// ignored so the report can grow without breaking the screen.
type tvLLMCacheSource struct {
	Hours int `json:"hours"`
	Total struct {
		Requests    int64    `json:"requests"`
		HitAll      *float64 `json:"hit_all"`
		HitReusable *float64 `json:"hit_reusable"`
	} `json:"total"`
	Savings struct {
		CloudRub *int64 `json:"cloud_rub"`
		SpentRub *int64 `json:"spent_rub"`
		SavedRub *int64 `json:"saved_rub"`
	} `json:"savings"`
	Hourly []struct {
		TS          int64    `json:"ts"`
		Requests    int64    `json:"requests"`
		HitAll      *float64 `json:"hit_all"`
		HitReusable *float64 `json:"hit_reusable"`
	} `json:"hourly"`
	GeneratedAt int64 `json:"generated_at"`
}

func (src tvLLMCacheSource) summary() *TVLLMCache {
	out := &TVLLMCache{
		Hours:       src.Hours,
		Requests:    src.Total.Requests,
		HitReusable: src.Total.HitReusable,
		HitAll:      src.Total.HitAll,
		Target:      tvLLMCacheTarget,
		CloudRub:    src.Savings.CloudRub,
		SpentRub:    src.Savings.SpentRub,
		SavedRub:    src.Savings.SavedRub,
		Hourly:      []TVLLMCacheHour{},
	}
	if src.GeneratedAt > 0 {
		out.ComputedAt = time.Unix(src.GeneratedAt, 0).UTC().Format(time.RFC3339)
	}
	for _, h := range src.Hourly {
		out.Hourly = append(out.Hourly, TVLLMCacheHour{
			At:          time.Unix(h.TS, 0).UTC().Format(time.RFC3339),
			Requests:    h.Requests,
			HitReusable: h.HitReusable,
			HitAll:      h.HitAll,
		})
	}
	return out
}

type tvLLMCacheState struct {
	mu        sync.Mutex
	checkedAt time.Time
	okAt      time.Time
	last      *TVLLMCache
}

var tvLLMCaches sync.Map // url -> *tvLLMCacheState

func tvLLMCacheFor(url string) *tvLLMCacheState {
	s, _ := tvLLMCaches.LoadOrStore(url, &tvLLMCacheState{})
	return s.(*tvLLMCacheState)
}

// get asks the source at most once per TTL, failed attempts included, so a
// source that is down costs one timeout a minute, not one per screen refresh.
func (s *tvLLMCacheState) get(ctx context.Context, url string, now time.Time, fetch func(context.Context, string) (*TVLLMCache, error)) *TVLLMCache {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.checkedAt.IsZero() || now.Sub(s.checkedAt) >= tvLLMCacheTTL {
		s.checkedAt = now
		if got, err := fetch(ctx, url); err != nil {
			slog.Warn("tv llm cache: source unavailable", "error", err)
		} else {
			s.last, s.okAt = got, now
		}
	}
	if s.last == nil || now.Sub(s.okAt) > tvLLMCacheStale {
		return nil
	}
	return s.last
}

func tvLLMCacheFetch(ctx context.Context, url string) (*TVLLMCache, error) {
	ctx, cancel := context.WithTimeout(ctx, tvProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := tvHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	var src tvLLMCacheSource
	if err := json.NewDecoder(io.LimitReader(resp.Body, tvLLMCacheMaxBody)).Decode(&src); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return src.summary(), nil
}

// tvLLMCache is the block for the overview, nil when it is not configured or
// has nothing current to show.
func (h *Handler) tvLLMCache(ctx context.Context, now time.Time) *TVLLMCache {
	url := h.cfg.TV.LLMCacheURL
	if url == "" {
		return nil
	}
	return tvLLMCacheFor(url).get(ctx, url, now, tvLLMCacheFetch)
}
