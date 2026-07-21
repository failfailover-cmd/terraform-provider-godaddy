package provider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewRequestLimiterUsesConfiguredRate(t *testing.T) {
	limiter := newRequestLimiter(40)
	if want := 1500 * time.Millisecond; limiter.interval != want {
		t.Fatalf("interval = %s, want %s", limiter.interval, want)
	}
}

func TestRequestLimiterRespectsCanceledContext(t *testing.T) {
	limiter := &requestLimiter{interval: time.Hour, next: time.Now().Add(time.Hour)}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := limiter.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait error = %v, want context.Canceled", err)
	}
}

func TestDoJSONWaitsForLimiterBeforeSendingRequest(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	resource := &domainRecordResource{cfg: &providerConfig{
		HTTPClient:     server.Client(),
		RequestLimiter: &requestLimiter{interval: time.Hour, next: time.Now().Add(time.Hour)},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, _, err := resource.doJSON(ctx, http.MethodGet, server.URL, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("doJSON error = %v, want context deadline exceeded", err)
	}
	if requests != 0 {
		t.Fatalf("server received %d requests, want 0", requests)
	}
}

func TestShouldRetry(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout} {
		if !shouldRetry(status) {
			t.Fatalf("shouldRetry(%d) = false, want true", status)
		}
	}
	if shouldRetry(http.StatusUnauthorized) {
		t.Fatal("shouldRetry(401) = true, want false")
	}
}
