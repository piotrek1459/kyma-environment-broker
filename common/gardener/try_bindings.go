package gardener

import (
	"log/slog"
	"math/rand/v2"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const maxBindingAttempts = 4

// TryWithBindings tries up to maxBindingAttempts CredentialsBindings from items, in order:
// first → middle → last → one random fallback. This avoids correlated failures when the
// first alphabetically-sorted binding has expired credentials.
func TryWithBindings[T any](items []unstructured.Unstructured, log *slog.Logger, tryFn func(*CredentialsBinding) (T, error)) (T, error) {
	candidates := selectCandidates(items)

	var lastErr error
	for _, idx := range candidates {
		cb := NewCredentialsBinding(items[idx])
		result, err := tryFn(cb)
		if err == nil {
			return result, nil
		}
		log.Warn("credentials binding attempt failed, trying next", "credentialBinding", cb.GetName(), "error", err)
		lastErr = err
	}

	var zero T
	return zero, lastErr
}

// selectCandidates returns up to maxBindingAttempts deduplicated indices from items.
// Deterministic candidates are [0, n/2, n-1]; if those exhaust the budget, a random
// index from the remainder fills the last slot.
func selectCandidates(items []unstructured.Unstructured) []int {
	n := len(items)
	if n == 0 {
		return nil
	}

	seen := make(map[int]bool)
	var candidates []int

	for _, idx := range []int{0, n / 2, n - 1} {
		if seen[idx] {
			continue
		}
		seen[idx] = true
		candidates = append(candidates, idx)
		if len(candidates) >= maxBindingAttempts {
			return candidates
		}
	}

	if len(candidates) >= maxBindingAttempts || len(candidates) >= n {
		return candidates
	}

	remaining := make([]int, 0, n-len(seen))
	for i := range n {
		if !seen[i] {
			remaining = append(remaining, i)
		}
	}
	rand.Shuffle(len(remaining), func(i, j int) { remaining[i], remaining[j] = remaining[j], remaining[i] })
	candidates = append(candidates, remaining[0])

	return candidates
}
