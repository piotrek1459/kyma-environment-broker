package gardener

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, nil))
}

// recordingValidator records which bindings were tried and optionally fails on specific names.
type recordingValidator struct {
	tried   []string
	failOn  map[string]bool
	callErr error
}

func (v *recordingValidator) Validate(_ context.Context, cb *CredentialsBinding) error {
	v.tried = append(v.tried, cb.GetName())
	if v.callErr != nil {
		return v.callErr
	}
	if v.failOn[cb.GetName()] {
		return fmt.Errorf("validator: binding %s is invalid", cb.GetName())
	}
	return nil
}

func makeBindingItems(names ...string) []unstructured.Unstructured {
	items := make([]unstructured.Unstructured, len(names))
	for i, name := range names {
		u := unstructured.Unstructured{}
		u.SetName(name)
		items[i] = u
	}
	return items
}

func TestFindValidBinding_EmptyItems(t *testing.T) {
	v := &recordingValidator{}
	_, err := FindValidBinding(context.Background(), nil, testLogger(), v)
	require.Error(t, err)
	assert.Empty(t, v.tried)
}

func TestFindValidBinding_SingleItem_Succeeds(t *testing.T) {
	items := makeBindingItems("cb-0")
	v := &recordingValidator{}
	cb, err := FindValidBinding(context.Background(), items, testLogger(), v)
	require.NoError(t, err)
	assert.Equal(t, "cb-0", cb.GetName())
	assert.Equal(t, []string{"cb-0"}, v.tried)
}

func TestFindValidBinding_FirstFails_SecondSucceeds(t *testing.T) {
	items := makeBindingItems("cb-bad", "cb-good")
	v := &recordingValidator{failOn: map[string]bool{"cb-bad": true}}
	cb, err := FindValidBinding(context.Background(), items, testLogger(), v)
	require.NoError(t, err)
	assert.Equal(t, "cb-good", cb.GetName())
	assert.Contains(t, v.tried, "cb-bad")
	assert.Contains(t, v.tried, "cb-good")
}

func TestFindValidBinding_AllFail_ReturnsLastError(t *testing.T) {
	items := makeBindingItems("cb-0", "cb-1", "cb-2")
	v := &recordingValidator{callErr: fmt.Errorf("auth error")}
	_, err := FindValidBinding(context.Background(), items, testLogger(), v)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "auth error")
	assert.Equal(t, 3, len(v.tried), "all 3 bindings should be tried")
}

func TestFindValidBinding_MaxFourAttempts(t *testing.T) {
	items := makeBindingItems("cb-0", "cb-1", "cb-2", "cb-3", "cb-4", "cb-5")
	v := &recordingValidator{callErr: fmt.Errorf("always fail")}
	_, _ = FindValidBinding(context.Background(), items, testLogger(), v)
	assert.LessOrEqual(t, len(v.tried), maxBindingAttempts)
}

func TestFindValidBinding_NoDuplicates(t *testing.T) {
	for n := 1; n <= 8; n++ {
		names := make([]string, n)
		for i := range names {
			names[i] = fmt.Sprintf("cb-%d", i)
		}
		v := &recordingValidator{callErr: fmt.Errorf("always fail")}
		_, _ = FindValidBinding(context.Background(), makeBindingItems(names...), testLogger(), v)

		seen := make(map[string]bool)
		for _, name := range v.tried {
			assert.False(t, seen[name], "n=%d: duplicate binding %s tried", n, name)
			seen[name] = true
		}
	}
}

func TestFindValidBinding_MiddleSucceeds(t *testing.T) {
	// 5 items: first and last fail, middle (index 2 = "cb-2") succeeds
	items := makeBindingItems("cb-0", "cb-1", "cb-2", "cb-3", "cb-4")
	v := &recordingValidator{failOn: map[string]bool{"cb-0": true, "cb-4": true}}
	cb, err := FindValidBinding(context.Background(), items, testLogger(), v)
	require.NoError(t, err)
	assert.Equal(t, "cb-2", cb.GetName())
}

func TestBindingProvider_CachesGoodBinding(t *testing.T) {
	items := makeBindingItems("cb-0")
	v := &recordingValidator{}
	p := NewBindingProvider(items, testLogger())

	cb1, err := p.Get(context.Background(), v)
	require.NoError(t, err)

	cb2, err := p.Get(context.Background(), v)
	require.NoError(t, err)

	assert.Equal(t, cb1.GetName(), cb2.GetName())
	// Second Get re-validates cached — tried twice total
	assert.Equal(t, 2, len(v.tried))
}

func TestBindingProvider_RefreshesWhenCachedFails(t *testing.T) {
	items := makeBindingItems("cb-bad", "cb-good")
	callCount := 0
	v := &recordingValidator{}

	// First call: cb-bad is the cached candidate (first in list), succeeds initially
	// We simulate it by getting cb-good first, then making cb-bad fail on re-validation.
	// Simpler: use a stateful validator.
	stateful := &statefulValidator{failAfterFirst: "cb-bad"}
	p := NewBindingProvider(items, testLogger())

	// First Get: cb-bad passes (first call) → cached
	cb1, err := p.Get(context.Background(), stateful)
	require.NoError(t, err)
	assert.Equal(t, "cb-bad", cb1.GetName())

	// Second Get: cb-bad fails (second call) → search → cb-good wins → cached
	cb2, err := p.Get(context.Background(), stateful)
	require.NoError(t, err)
	assert.Equal(t, "cb-good", cb2.GetName())

	_ = callCount
	_ = v
}

// statefulValidator fails a named binding after the first successful call.
type statefulValidator struct {
	failAfterFirst string
	seen           map[string]int
}

func (v *statefulValidator) Validate(_ context.Context, cb *CredentialsBinding) error {
	if v.seen == nil {
		v.seen = make(map[string]int)
	}
	v.seen[cb.GetName()]++
	if cb.GetName() == v.failAfterFirst && v.seen[cb.GetName()] > 1 {
		return fmt.Errorf("binding %s expired", cb.GetName())
	}
	return nil
}

func TestSelectCandidates_NoDuplicates(t *testing.T) {
	for n := 1; n <= 10; n++ {
		got := selectCandidates(n)
		seen := make(map[int]bool)
		for _, idx := range got {
			assert.False(t, seen[idx], "n=%d: duplicate index %d", n, idx)
			seen[idx] = true
			assert.Less(t, idx, n)
		}
		assert.LessOrEqual(t, len(got), maxBindingAttempts)
	}
}

func TestSelectCandidates_One(t *testing.T) {
	assert.Equal(t, []int{0}, selectCandidates(1))
}

func TestSelectCandidates_Two(t *testing.T) {
	assert.Equal(t, []int{0, 1}, selectCandidates(2))
}

func TestSelectCandidates_Three(t *testing.T) {
	assert.Equal(t, []int{0, 1, 2}, selectCandidates(3))
}

func TestSelectCandidates_Five_FirstMiddleLast(t *testing.T) {
	got := selectCandidates(5)
	assert.Equal(t, 0, got[0])
	assert.Equal(t, 2, got[1])
	assert.Equal(t, 4, got[2])
	assert.Contains(t, []int{1, 3}, got[3])
}
