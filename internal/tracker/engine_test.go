package tracker

import "testing"

func TestDefaultWorkersAppliesLimits(t *testing.T) {
	t.Parallel()

	if got := defaultWorkers(0, 0); got != 1 {
		t.Fatalf("expected fallback to minimum 1 worker, got %d", got)
	}
	if got := defaultWorkers(0, 10); got != 10 {
		t.Fatalf("expected target count as default, got %d", got)
	}
	if got := defaultWorkers(10_000, 10_000); got != maxParallelChecksHardLimit {
		t.Fatalf("expected hard limit %d, got %d", maxParallelChecksHardLimit, got)
	}
}
