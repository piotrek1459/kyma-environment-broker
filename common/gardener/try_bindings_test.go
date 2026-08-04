package gardener

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"log/slog"
	"os"
)

func testLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, nil))
}

func makeItems(names ...string) []unstructured.Unstructured {
	items := make([]unstructured.Unstructured, len(names))
	for i, name := range names {
		u := unstructured.Unstructured{}
		u.SetName(name)
		items[i] = u
	}
	return items
}

func TestTryWithBindings_SingleItem_Success(t *testing.T) {
	items := makeItems("cb-0")
	result, err := TryWithBindings(items, testLog(), func(cb *CredentialsBinding) (string, error) {
		return cb.GetName(), nil
	})
	require.NoError(t, err)
	assert.Equal(t, "cb-0", result)
}

func TestTryWithBindings_EmptyItems_ReturnsNilError(t *testing.T) {
	_, err := TryWithBindings([]unstructured.Unstructured{}, testLog(), func(cb *CredentialsBinding) (string, error) {
		return cb.GetName(), nil
	})
	assert.NoError(t, err, "empty list: tryFn never called, no error expected")
}

func TestTryWithBindings_FirstFails_SecondSucceeds(t *testing.T) {
	items := makeItems("cb-bad", "cb-good")
	var tried []string
	result, err := TryWithBindings(items, testLog(), func(cb *CredentialsBinding) (string, error) {
		tried = append(tried, cb.GetName())
		if cb.GetName() == "cb-bad" {
			return "", fmt.Errorf("auth error")
		}
		return cb.GetName(), nil
	})
	require.NoError(t, err)
	assert.Equal(t, "cb-good", result)
	assert.Contains(t, tried, "cb-bad")
	assert.Contains(t, tried, "cb-good")
}

func TestTryWithBindings_AllFail_ReturnsLastError(t *testing.T) {
	items := makeItems("cb-0", "cb-1", "cb-2")
	callCount := 0
	_, err := TryWithBindings(items, testLog(), func(cb *CredentialsBinding) (string, error) {
		callCount++
		return "", fmt.Errorf("error from %s", cb.GetName())
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cb-")
	assert.Equal(t, 3, callCount, "exactly 3 items = 3 candidates, all tried")
}

func TestTryWithBindings_FiveItems_TriesAtMostFour(t *testing.T) {
	items := makeItems("cb-0", "cb-1", "cb-2", "cb-3", "cb-4")
	callCount := 0
	_, _ = TryWithBindings(items, testLog(), func(cb *CredentialsBinding) (string, error) {
		callCount++
		return "", fmt.Errorf("always fail")
	})
	assert.LessOrEqual(t, callCount, maxBindingAttempts)
}

func TestTryWithBindings_FiveItems_NoDuplicateBindings(t *testing.T) {
	items := makeItems("cb-0", "cb-1", "cb-2", "cb-3", "cb-4")
	tried := make(map[string]int)
	_, _ = TryWithBindings(items, testLog(), func(cb *CredentialsBinding) (string, error) {
		tried[cb.GetName()]++
		return "", fmt.Errorf("always fail")
	})
	for name, count := range tried {
		assert.Equal(t, 1, count, "binding %s tried more than once", name)
	}
}

func TestTryWithBindings_MiddleBindingSucceeds(t *testing.T) {
	// 5 items: first and last fail, middle (index 2) succeeds
	items := makeItems("cb-0", "cb-1", "cb-2", "cb-3", "cb-4")
	result, err := TryWithBindings(items, testLog(), func(cb *CredentialsBinding) (string, error) {
		if cb.GetName() == "cb-2" {
			return "ok", nil
		}
		return "", fmt.Errorf("fail")
	})
	require.NoError(t, err)
	assert.Equal(t, "ok", result)
}

func TestSelectCandidates_One(t *testing.T) {
	assert.Equal(t, []int{0}, selectCandidates(makeItems("a")))
}

func TestSelectCandidates_Two(t *testing.T) {
	got := selectCandidates(makeItems("a", "b"))
	assert.Equal(t, []int{0, 1}, got)
}

func TestSelectCandidates_Three(t *testing.T) {
	got := selectCandidates(makeItems("a", "b", "c"))
	assert.Equal(t, []int{0, 1, 2}, got)
}

func TestSelectCandidates_Four(t *testing.T) {
	// n=4: deterministic: 0, 2, 3 (middle=2, last=3) — 3 candidates
	// one random from {1}
	got := selectCandidates(makeItems("a", "b", "c", "d"))
	assert.Len(t, got, 4)
	assert.Equal(t, got[0], 0)
	assert.Equal(t, got[1], 2)
	assert.Equal(t, got[2], 3)
	assert.Equal(t, got[3], 1, "only remaining index is 1")
}

func TestSelectCandidates_Five(t *testing.T) {
	got := selectCandidates(makeItems("a", "b", "c", "d", "e"))
	assert.Len(t, got, 4)
	assert.Equal(t, 0, got[0])
	assert.Equal(t, 2, got[1])
	assert.Equal(t, 4, got[2])
	// 4th is a random from {1, 3}
	assert.Contains(t, []int{1, 3}, got[3])
}

func TestSelectCandidates_NoDuplicates(t *testing.T) {
	for n := 1; n <= 10; n++ {
		names := make([]string, n)
		for i := range names {
			names[i] = fmt.Sprintf("cb-%d", i)
		}
		got := selectCandidates(makeItems(names...))
		seen := make(map[int]bool)
		for _, idx := range got {
			assert.False(t, seen[idx], "n=%d: duplicate index %d", n, idx)
			seen[idx] = true
		}
		assert.LessOrEqual(t, len(got), maxBindingAttempts, "n=%d: too many candidates", n)
	}
}
