package updates

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type mockCleaner struct {
	callCount int
	failUntil int
	id        int
}

func (m *mockCleaner) Clean(_ context.Context) error {
	m.callCount++
	if m.callCount <= m.failUntil {
		return errors.New("not ready yet")
	}

	return nil
}

func (m *mockCleaner) UpdateID() int {
	return m.id
}

func TestRetry_SucceedsOnFirstAttempt(t *testing.T) {
	t.Parallel()

	calls := 0
	err := retry(t.Context(), 3, 0, func(_ context.Context) error {
		calls++
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, calls)
}

func TestRetry_SucceedsAfterRetries(t *testing.T) {
	t.Parallel()

	calls := 0
	err := retry(t.Context(), 3, 0, func(_ context.Context) error {
		calls++
		if calls < 3 {
			return errors.New("temporary error")
		}

		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 3, calls)
}

func TestRetry_ExhaustsAllAttempts(t *testing.T) {
	t.Parallel()

	calls := 0
	sentinel := errors.New("always fails")
	err := retry(t.Context(), 3, 0, func(_ context.Context) error {
		calls++
		return sentinel
	})
	require.ErrorIs(t, err, sentinel)
	require.Equal(t, 3, calls)
}

func TestRemove_CleanerSucceeds(t *testing.T) {
	t.Parallel()

	cleaner := &mockCleaner{id: 42}
	err := Remove(t.Context(), cleaner)
	require.NoError(t, err)
	require.Equal(t, 1, cleaner.callCount)
}

func TestUpdateID_DefaultIsZero(t *testing.T) {
	t.Parallel()

	atomicUpdateID.Store(0)
	require.Equal(t, 0, UpdateID())
}

func TestSetUpdateID_RoundTrip(t *testing.T) {
	t.Parallel()

	original := int(atomicUpdateID.Load())
	t.Cleanup(func() { atomicUpdateID.Store(int32(original)) })

	SetUpdateID(1234)
	require.Equal(t, 1234, UpdateID())
}

func TestAgentUpdateCleanup_ZeroUpdateID(t *testing.T) {
	t.Parallel()

	err := AgentUpdateCleanup(t.Context(), 0)
	require.NoError(t, err)
}

func TestRetry_ZeroMaxRetries(t *testing.T) {
	t.Parallel()

	calls := 0
	err := retry(t.Context(), 0, 0, func(_ context.Context) error {
		calls++
		return errors.New("should not be called")
	})
	require.NoError(t, err)
	require.Equal(t, 0, calls)
}
