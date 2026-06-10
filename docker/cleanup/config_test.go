package cleanup

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDurationUnmarshalJSON_Milliseconds(t *testing.T) {
	var d Duration
	require.NoError(t, json.Unmarshal([]byte(`3600000`), &d))
	assert.Equal(t, time.Hour, d.Std())
}

func TestDurationUnmarshalJSON_Invalid(t *testing.T) {
	var d Duration
	err := json.Unmarshal([]byte(`true`), &d)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duration must be a JSON millisecond integer")
}

func TestDurationUnmarshalJSON_StringRejected(t *testing.T) {
	var d Duration
	err := json.Unmarshal([]byte(`"24h"`), &d)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duration must be a JSON millisecond integer")
}

func TestDurationMarshalJSON(t *testing.T) {
	b, err := json.Marshal(Duration(90 * time.Minute))
	require.NoError(t, err)
	assert.Equal(t, `5400000`, string(b))
}

func TestDecodeConfig_MillisecondDurations(t *testing.T) {
	raw := json.RawMessage(`{
		"cleanupInterval": 86400000,
		"cleanOldImages": true,
		"imageMaximumAgeForGC": 2592000000,
		"cleanImagesAtStorageLimit": true,
		"cleanupStartHostThresholdPercent": 80,
		"cleanupEndHostThresholdPercent": 70,
		"maximumImagesPerInterval": 10,
		"minAge": 86400000,
		"excludedImages": ["nginx:latest"]
	}`)

	cfg, err := DecodeConfig(raw)
	require.NoError(t, err)
	assert.Equal(t, 24*time.Hour, cfg.CleanupInterval.Std())
	assert.True(t, cfg.CleanOldImages)
	assert.Equal(t, 30*24*time.Hour, cfg.ImageMaximumAgeForGC.Std())
	assert.True(t, cfg.CleanImagesAtStorageLimit)
	assert.Equal(t, 24*time.Hour, cfg.MinAge.Std())
	assert.Equal(t, []string{"nginx:latest"}, cfg.ExcludedImages)
}

func TestDecodeConfig_Invalid(t *testing.T) {
	_, err := DecodeConfig(json.RawMessage(`{"cleanupInterval": true}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid cleanup config")
}

func TestDecodeConfig_Validate(t *testing.T) {
	tests := []struct {
		name   string
		raw    string
		errMsg string
	}{
		{
			name:   "negative cleanup interval",
			raw:    `{"cleanupInterval": -1000}`,
			errMsg: "cleanupInterval must be at least 5 minutes",
		},
		{
			name:   "zero cleanup interval",
			raw:    `{"cleanupInterval": 0}`,
			errMsg: "cleanupInterval must be at least 5 minutes",
		},
		{
			name:   "cleanup interval below 5 minutes",
			raw:    `{"cleanupInterval": 240000}`,
			errMsg: "cleanupInterval must be at least 5 minutes",
		},
		{
			name:   "negative imageMaximumAgeForGC",
			raw:    `{"cleanupInterval": 86400000, "imageMaximumAgeForGC": -1}`,
			errMsg: "imageMaximumAgeForGC must not be negative",
		},
		{
			name:   "cleanOldImages enabled with zero imageMaximumAgeForGC",
			raw:    `{"cleanupInterval": 86400000, "cleanOldImages": true, "imageMaximumAgeForGC": 0}`,
			errMsg: "imageMaximumAgeForGC must be a positive duration when cleanOldImages is enabled",
		},
		{
			name:   "negative minAge",
			raw:    `{"cleanupInterval": 86400000, "minAge": -500}`,
			errMsg: "minAge must not be negative",
		},
		{
			name:   "cleanImagesAtStorageLimit enabled with start threshold above 100",
			raw:    `{"cleanupInterval": 86400000, "cleanImagesAtStorageLimit": true, "cleanupStartHostThresholdPercent": 101, "cleanupEndHostThresholdPercent": 70}`,
			errMsg: "cleanupStartHostThresholdPercent must be between 1 and 100 when cleanImagesAtStorageLimit is enabled",
		},
		{
			name:   "cleanImagesAtStorageLimit enabled with end threshold above 100",
			raw:    `{"cleanupInterval": 86400000, "cleanImagesAtStorageLimit": true, "cleanupStartHostThresholdPercent": 80, "cleanupEndHostThresholdPercent": 110}`,
			errMsg: "cleanupEndHostThresholdPercent must be between 1 and 100 when cleanImagesAtStorageLimit is enabled",
		},
		{
			name:   "cleanImagesAtStorageLimit enabled with end threshold not less than start threshold",
			raw:    `{"cleanupInterval": 86400000, "cleanImagesAtStorageLimit": true, "cleanupStartHostThresholdPercent": 70, "cleanupEndHostThresholdPercent": 80}`,
			errMsg: "cleanupEndHostThresholdPercent must be less than cleanupStartHostThresholdPercent when cleanImagesAtStorageLimit is enabled",
		},
		{
			name:   "cleanImagesAtStorageLimit enabled with end threshold equal to start threshold",
			raw:    `{"cleanupInterval": 86400000, "cleanImagesAtStorageLimit": true, "cleanupStartHostThresholdPercent": 80, "cleanupEndHostThresholdPercent": 80}`,
			errMsg: "cleanupEndHostThresholdPercent must be less than cleanupStartHostThresholdPercent when cleanImagesAtStorageLimit is enabled",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeConfig(json.RawMessage(tc.raw))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "invalid cleanup config")
			assert.Contains(t, err.Error(), tc.errMsg)
		})
	}
}
