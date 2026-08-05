package gardener

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const maxBindingAttempts = 4

// BindingValidator validates a CredentialsBinding — typically by fetching its secret
// and verifying the credentials work against the cloud provider.
// Implementations store their result so callers can reuse it without a second fetch.
type BindingValidator interface {
	Validate(ctx context.Context, cb *CredentialsBinding) error
}

// FindValidBinding iterates over items in order: first → middle → last → one random fallback,
// trying each with validator. Returns the first binding that passes, or an error if all fail.
// Max 4 attempts regardless of list length.
func FindValidBinding(ctx context.Context, items []unstructured.Unstructured, log *slog.Logger, validator BindingValidator) (*CredentialsBinding, error) {
	if len(items) == 0 {
		return nil, fmt.Errorf("no credentials bindings to try")
	}

	var lastErr error
	for _, idx := range selectCandidates(len(items)) {
		cb := NewCredentialsBinding(items[idx])
		if err := validator.Validate(ctx, cb); err != nil {
			log.Warn("credentials binding attempt failed, trying next", "credentialBinding", cb.GetName(), "error", err)
			lastErr = err
			continue
		}
		return cb, nil
	}
	return nil, lastErr
}

// BindingProvider caches the last known-good CredentialsBinding. On Get, it re-validates
// the cached binding; if validation fails it searches for a new one via FindValidBinding.
// Thread-safe — safe to call from multiple goroutines (e.g. Azure cache refresh).
type BindingProvider struct {
	items  []unstructured.Unstructured
	log    *slog.Logger
	mu     sync.Mutex
	cached *CredentialsBinding
}

func NewBindingProvider(items []unstructured.Unstructured, log *slog.Logger) *BindingProvider {
	return &BindingProvider{items: items, log: log}
}

// Get returns a valid CredentialsBinding. If the cached binding still passes validation
// it is returned immediately. Otherwise a new search is performed and the winner is cached.
func (p *BindingProvider) Get(ctx context.Context, validator BindingValidator) (*CredentialsBinding, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cached != nil {
		if err := validator.Validate(ctx, p.cached); err != nil {
			p.log.Warn("cached binding failed validation, searching for a new one", "credentialBinding", p.cached.GetName(), "error", err)
			p.cached = nil
		} else {
			return p.cached, nil
		}
	}

	cb, err := FindValidBinding(ctx, p.items, p.log, validator)
	if err != nil {
		return nil, err
	}
	p.cached = cb
	return cb, nil
}

// selectCandidates returns up to maxBindingAttempts deduplicated indices.
// Deterministic: [0, n/2, n-1]; if budget remains, one random from the rest.
func selectCandidates(n int) []int {
	if n == 0 {
		return nil
	}

	seen := make(map[int]bool)
	var out []int

	for _, idx := range []int{0, n / 2, n - 1} {
		if seen[idx] {
			continue
		}
		seen[idx] = true
		out = append(out, idx)
		if len(out) >= maxBindingAttempts {
			return out
		}
	}

	if len(out) >= n {
		return out
	}

	remaining := make([]int, 0, n-len(seen))
	for i := range n {
		if !seen[i] {
			remaining = append(remaining, i)
		}
	}
	rand.Shuffle(len(remaining), func(i, j int) { remaining[i], remaining[j] = remaining[j], remaining[i] })
	out = append(out, remaining[0])
	return out
}
