package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"
)

func TestRunWithCopyRetryEventuallySucceeds(t *testing.T) {
	attempts := 0
	err := runWithCopyRetry(
		context.Background(),
		"test copy",
		copyRetryPolicy{
			Retries:      3,
			InitialDelay: time.Millisecond,
			MaxDelay:     2 * time.Millisecond,
		},
		func() error {
			attempts++
			if attempts < 3 {
				return fmt.Errorf("pulling layers: %w", io.ErrUnexpectedEOF)
			}
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

func TestRunWithCopyRetryDoesNotRetryPermanentError(t *testing.T) {
	attempts := 0
	permanent := errors.New("unauthorized")
	err := runWithCopyRetry(
		context.Background(),
		"test copy",
		copyRetryPolicy{
			Retries:      4,
			InitialDelay: time.Millisecond,
			MaxDelay:     time.Millisecond,
		},
		func() error {
			attempts++
			return permanent
		},
	)
	if !errors.Is(err, permanent) {
		t.Fatalf("error = %v", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

func TestRetryDelayUsesExponentialBackoffAndCap(t *testing.T) {
	policy := copyRetryPolicy{
		Retries:      5,
		InitialDelay: 2 * time.Second,
		MaxDelay:     10 * time.Second,
	}
	want := []time.Duration{
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		10 * time.Second,
		10 * time.Second,
	}
	for i, expected := range want {
		if got := retryDelay(policy, i+1); got != expected {
			t.Errorf("retryDelay(%d) = %s, want %s", i+1, got, expected)
		}
	}
}

func TestIsRetryableCopyError(t *testing.T) {
	if !isRetryableCopyError(fmt.Errorf("pulling layers: %w", io.ErrUnexpectedEOF)) {
		t.Fatal("unexpected EOF should be retryable")
	}
	if !isRetryableCopyError(errors.New("read: operation timed out")) {
		t.Fatal("operation timeout should be retryable")
	}
	if !isRetryableCopyError(fmt.Errorf("request failed: %w", context.DeadlineExceeded)) {
		t.Fatal("request deadline should be retryable")
	}
	if isRetryableCopyError(errors.New("UNAUTHORIZED: login required")) {
		t.Fatal("authentication error should not be retryable")
	}
}

func TestValidateCopyRetryPolicy(t *testing.T) {
	err := validateCopyRetryPolicy(copyRetryPolicy{
		Retries:      1,
		InitialDelay: 5 * time.Second,
		MaxDelay:     2 * time.Second,
	})
	if err == nil {
		t.Fatal("expected invalid backoff range")
	}
}
