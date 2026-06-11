package kubernetes

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const (
	testLatencyLe100ms = "0.1"
	testLatencyLe200ms = "0.2"
	testLatencyLe500ms = "0.5"
	testLatencyLeInf   = "+Inf"
)

type latencyFixtureSeries struct {
	Verb     string
	Resource string
	Count    float64
	Buckets  map[string]float64
}

func TestParseAPIServerRequestLatencyHistogram_PrefersSLIAndPoolsSeries(t *testing.T) {
	t.Parallel()

	payload := buildAPIServerRequestLatencyPayload(map[string][]latencyFixtureSeries{
		apiServerRequestLatencySLIFamily: {
			{
				Verb:     "POST",
				Resource: "pods",
				Count:    100,
				Buckets:  map[string]float64{testLatencyLe100ms: 40, testLatencyLe500ms: 95, testLatencyLeInf: 100},
			},
			{
				Verb:     "LIST",
				Resource: "events",
				Count:    20,
				Buckets:  map[string]float64{testLatencyLe100ms: 10, testLatencyLe500ms: 18, testLatencyLeInf: 20},
			},
		},
		// SLO family is present but must be ignored when SLI exists.
		apiServerRequestLatencySLOFamily: {
			{
				Verb:     "POST",
				Resource: "pods",
				Count:    999,
				Buckets:  map[string]float64{testLatencyLe100ms: 1, testLatencyLeInf: 999},
			},
		},
	})

	histogram, err := parseAPIServerRequestLatencyHistogram(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantBuckets := map[float64]float64{0.1: 50, 0.5: 113, math.Inf(1): 120}
	assertBucketsEqual(t, wantBuckets, histogram.Buckets)
	if histogram.Count != 120 {
		t.Fatalf("count mismatch: got %v, want 120", histogram.Count)
	}
}

func TestParseAPIServerRequestLatencyHistogram_FallsBackToSLO(t *testing.T) {
	t.Parallel()

	payload := buildAPIServerRequestLatencyPayload(map[string][]latencyFixtureSeries{
		apiServerRequestLatencySLOFamily: {
			{
				Verb:     "GET",
				Resource: "configmaps",
				Count:    50,
				Buckets:  map[string]float64{testLatencyLe200ms: 30, testLatencyLeInf: 50},
			},
		},
	})

	histogram, err := parseAPIServerRequestLatencyHistogram(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertBucketsEqual(t, map[float64]float64{0.2: 30, math.Inf(1): 50}, histogram.Buckets)
	if histogram.Count != 50 {
		t.Fatalf("count mismatch: got %v, want 50", histogram.Count)
	}
}

func TestParseAPIServerRequestLatencyHistogram_UnsupportedWhenNoFamiliesFound(t *testing.T) {
	t.Parallel()

	payload := []byte("some_other_metric{verb=\"GET\"} 1\n")

	_, err := parseAPIServerRequestLatencyHistogram(payload)
	if !errors.Is(err, ErrAPIServerRequestLatencyUnsupported) {
		t.Fatalf("expected ErrAPIServerRequestLatencyUnsupported, got %v", err)
	}
}

func assertBucketsEqual(t *testing.T, want, got map[float64]float64) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("bucket count mismatch: got %v, want %v", got, want)
	}

	for le, wantCount := range want {
		if got[le] != wantCount {
			t.Fatalf("bucket le=%v mismatch: got %v, want %v", le, got[le], wantCount)
		}
	}
}

func buildAPIServerRequestLatencyPayload(families map[string][]latencyFixtureSeries) []byte {
	familyNames := make([]string, 0, len(families))
	for familyName := range families {
		familyNames = append(familyNames, familyName)
	}
	slices.Sort(familyNames)

	var b strings.Builder
	for _, familyName := range familyNames {
		series := slices.Clone(families[familyName])
		slices.SortFunc(series, func(a, c latencyFixtureSeries) int {
			if a.Verb != c.Verb {
				return strings.Compare(a.Verb, c.Verb)
			}
			return strings.Compare(a.Resource, c.Resource)
		})

		for _, item := range series {
			bucketBounds := make([]string, 0, len(item.Buckets))
			for bound := range item.Buckets {
				bucketBounds = append(bucketBounds, bound)
			}
			slices.SortFunc(bucketBounds, compareAPIServerLatencyBucketBounds)

			for _, bound := range bucketBounds {
				fmt.Fprintf(
					&b,
					"%s_bucket{verb=%q,resource=%q,le=%q} %v\n",
					familyName,
					item.Verb,
					item.Resource,
					bound,
					item.Buckets[bound],
				)
			}

			fmt.Fprintf(
				&b,
				"%s_count{verb=%q,resource=%q} %v\n",
				familyName,
				item.Verb,
				item.Resource,
				item.Count,
			)
		}
	}

	return []byte(b.String())
}

func compareAPIServerLatencyBucketBounds(a, b string) int {
	aLe := parseAPIServerLatencyBucketBound(a)
	bLe := parseAPIServerLatencyBucketBound(b)
	if aLe < bLe {
		return -1
	}
	if aLe > bLe {
		return 1
	}
	return 0
}

func parseAPIServerLatencyBucketBound(raw string) float64 {
	if raw == testLatencyLeInf {
		return math.Inf(1)
	}

	parsed, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0
	}

	return parsed
}
