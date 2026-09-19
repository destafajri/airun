package router

import "testing"

func TestClassifyProviderFailure(t *testing.T) {
	tests := []struct {
		name string
		text string
		want FailureKind
	}{
		{"quota", "You've hit your weekly usage limit", FailureQuota},
		{"rate", "HTTP 429 rate_limit exceeded", FailureRateLimit},
		{"timeout", "request timed out after 120s", FailureTimeout},
		{"outage", "service unavailable HTTP 503", FailureOutage},
		{"auth", "401 unauthorized invalid api key", FailureAuth},
		{"unknown", "unexpected provider failure", FailureProvider},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyProviderFailure(tt.text, nil); got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}

func TestClassifyProviderFailureUsesCustomPatternsFirst(t *testing.T) {
	custom := map[FailureKind][]string{FailureQuota: {"credits exhausted"}}
	if got := ClassifyProviderFailure("Credits Exhausted; upgrade plan", custom); got != FailureQuota {
		t.Fatalf("got %q want %q", got, FailureQuota)
	}
}
