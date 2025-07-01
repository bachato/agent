package health

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHealthy(t *testing.T) {
	withCleanup(t)
	healthy := Healthy()
	assert.False(t, healthy, "should be unhealthy")
	err := SetHealthy()
	assert.NoError(t, err)

	healthy = Healthy()
	assert.True(t, healthy, "should be healthy")
}

func TestSetHealthy(t *testing.T) {
	withCleanup(t)
	err := SetHealthy()
	assert.NoError(t, err)
	assert.True(t, Healthy(), "should be healthy")
	err = SetHealthy()
	assert.NoError(t, err, "should not error when already healthy, this verifies idempotency")
}

func TestSetUnhealthy(t *testing.T) {
	withCleanup(t)
	err := SetHealthy()
	assert.NoError(t, err)
	assert.True(t, Healthy(), "should be healthy")

	err = SetUnHealthy()
	assert.NoError(t, err)
	assert.False(t, Healthy(), "should be unhealthy")
	err = SetUnHealthy()
	assert.NoError(t, err, "should not error when already unhealthy, this verifies idempotency")
}

func TestConcurrency(t *testing.T) {
	withCleanup(t)

	const routines = 100
	var wg sync.WaitGroup
	wg.Add(routines * 2)

	errs := make(chan error, routines*2)

	// Half the routines call SetHealthy, half call SetUnHealthy
	for range routines {
		go func() {
			defer wg.Done()
			if err := SetHealthy(); err != nil {
				errs <- SetHealthy()
			}

		}()
		go func() {
			defer wg.Done()
			if err := SetUnHealthy(); err != nil {
				errs <- err
			}
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		assert.NoError(t, err, "unexpected error during concurrent access")
	}
}

func withCleanup(t *testing.T) {
	_ = SetUnHealthy()
	t.Cleanup(func() {
		err := SetUnHealthy()
		if err != nil {
			t.Errorf("failed to set unhealthy: %v", err)
		}
	})
}
