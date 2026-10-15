package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const tvLLMCacheSample = `{"hours": 24, "page": 1536,
 "total": {"requests": 713, "prompt": 100, "cached": 95, "hit_all": 94.92, "hit_reusable": 97.08},
 "by_key": {"secret-alias": {"requests": 1}},
 "savings": {"cloud_usd": 2.91, "cloud_rub": 245, "spent_rub": 233, "saved_rub": 11},
 "hourly": [{"ts": 1792065600, "hour_msk": "15.10 15:00", "requests": 6, "hit_all": 97.9, "hit_reusable": null}],
 "line": "…", "generated_at": 1792068468}`

func TestTvLLMCacheFetchKeepsOnlyScreenFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/report.json" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(tvLLMCacheSample))
	}))
	defer srv.Close()

	got, err := tvLLMCacheFetch(context.Background(), srv.URL+"/report.json")
	if err != nil {
		t.Fatal(err)
	}
	if got.Requests != 713 || *got.HitReusable != 97.08 || *got.HitAll != 94.92 || got.Target != 99.5 {
		t.Fatalf("totals: %+v", got)
	}
	if *got.CloudRub != 245 || *got.SpentRub != 233 || *got.SavedRub != 11 {
		t.Fatalf("savings: %+v", got)
	}
	if len(got.Hourly) != 1 || got.Hourly[0].At != "2026-10-15T12:00:00Z" || got.Hourly[0].HitReusable != nil || *got.Hourly[0].HitAll != 97.9 {
		t.Fatalf("hourly: %+v", got.Hourly)
	}
	if got.ComputedAt != "2026-10-15T12:47:48Z" {
		t.Fatalf("computed at: %q", got.ComputedAt)
	}

	if _, err := tvLLMCacheFetch(context.Background(), srv.URL+"/other"); err == nil {
		t.Fatal("non-200 must be an error")
	}
}

func TestTvLLMCacheStateCachesAndExpires(t *testing.T) {
	t0 := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	calls := 0
	fail := false
	fetch := func(context.Context, string) (*TVLLMCache, error) {
		calls++
		if fail {
			return nil, errors.New("down")
		}
		return &TVLLMCache{Requests: int64(calls)}, nil
	}
	s := &tvLLMCacheState{}
	if got := s.get(context.Background(), "u", t0, fetch); got == nil || got.Requests != 1 {
		t.Fatalf("first: %+v", got)
	}
	if got := s.get(context.Background(), "u", t0.Add(30*time.Second), fetch); got.Requests != 1 || calls != 1 {
		t.Fatalf("within TTL the source is not asked again: %+v, %d calls", got, calls)
	}
	fail = true
	if got := s.get(context.Background(), "u", t0.Add(2*time.Minute), fetch); got == nil || got.Requests != 1 || calls != 2 {
		t.Fatalf("a failed refresh keeps the last summary: %+v, %d calls", got, calls)
	}
	if got := s.get(context.Background(), "u", t0.Add(2*time.Minute+10*time.Second), fetch); calls != 2 || got == nil {
		t.Fatalf("a failed attempt also waits for the TTL: %d calls", calls)
	}
	if got := s.get(context.Background(), "u", t0.Add(31*time.Minute), fetch); got != nil {
		t.Fatalf("older than the stale limit hides the block: %+v", got)
	}
	if got := (&tvLLMCacheState{}).get(context.Background(), "u", t0, fetch); got != nil {
		t.Fatalf("never fetched: %+v", got)
	}
}
