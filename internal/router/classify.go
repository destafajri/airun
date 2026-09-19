package router

import "strings"

type FailureKind string

const (
	FailureQuota       FailureKind = "quota"
	FailureRateLimit   FailureKind = "rate_limit"
	FailureTimeout     FailureKind = "timeout"
	FailureOutage      FailureKind = "outage"
	FailureUnavailable FailureKind = "unavailable"
	FailureAuth        FailureKind = "auth"
	FailureProvider    FailureKind = "provider"
	FailureUnknown     FailureKind = "unknown"
)

var builtinPatterns = map[FailureKind][]string{
	FailureQuota:       {"usage limit", "weekly limit", "quota exceeded", "quota_exceeded", "credit balance too low", "credits exhausted"},
	FailureRateLimit:   {"rate limit", "rate_limit", "too many requests", "http 429", "status 429"},
	FailureTimeout:     {"timed out", "timeout", "deadline exceeded"},
	FailureOutage:      {"service unavailable", "http 503", "status 503", "bad gateway", "http 502", "status 502", "gateway timeout", "http 504"},
	FailureUnavailable: {"executable file not found", "command not found", "no such file or directory"},
	FailureAuth:        {"unauthorized", "invalid api key", "authentication failed", "http 401", "status 401", "forbidden", "http 403"},
}

func ClassifyProviderFailure(text string, custom map[FailureKind][]string) FailureKind {
	lower := strings.ToLower(text)
	order := []FailureKind{FailureQuota, FailureRateLimit, FailureTimeout, FailureOutage, FailureUnavailable, FailureAuth, FailureProvider}
	for _, kind := range order {
		for _, pattern := range custom[kind] {
			if strings.Contains(lower, strings.ToLower(pattern)) {
				return kind
			}
		}
	}
	for _, kind := range order {
		for _, pattern := range builtinPatterns[kind] {
			if strings.Contains(lower, pattern) {
				return kind
			}
		}
	}
	return FailureUnknown
}
