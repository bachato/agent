package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/model/textparse"
)

const (
	apiServerRequestLatencySLIFamily = "apiserver_request_sli_duration_seconds"
	apiServerRequestLatencySLOFamily = "apiserver_request_slo_duration_seconds"
)

var ErrAPIServerRequestLatencyUnsupported = errors.New("api server request latency histogram family is not available")

// APIServerLatencyHistogram is the API server request-duration histogram pooled
// across every verb/resource into a single cluster-wide classic histogram.
//
// The agent only parses and pools; quantile estimation is left to the evaluator,
// which re-exposes Buckets as le-labelled series and computes
// histogram_quantile(0.99, rate(...)) in the alert rule.
type APIServerLatencyHistogram struct {
	Buckets map[float64]float64 // le upper bound -> cumulative observation count
	Count   float64             // total observations (the +Inf bucket)
}

// CollectAPIServerRequestLatency scrapes the API server /metrics endpoint and
// returns its request-duration histogram pooled across all verbs and resources.
// It prefers the SLI family (which already excludes legitimately-slow WATCH and
// long LIST calls) and falls back to the SLO family.
func CollectAPIServerRequestLatency(ctx context.Context, kc *KubeClient) (APIServerLatencyHistogram, error) {
	if kc == nil {
		return APIServerLatencyHistogram{}, errors.New("kubernetes client is nil")
	}

	if kc.cli == nil {
		return APIServerLatencyHistogram{}, errors.New("kubernetes clientset is nil")
	}

	payload, requestErr := kc.cli.RESTClient().Get().
		AbsPath("/metrics").
		DoRaw(ctx)

	histogram, parseErr := parseAPIServerRequestLatencyHistogram(payload)
	if parseErr == nil {
		return histogram, nil
	}

	if requestErr != nil {
		return APIServerLatencyHistogram{}, fmt.Errorf("failed to query /metrics. Error: %w", requestErr)
	}

	return APIServerLatencyHistogram{}, parseErr
}

func parseAPIServerRequestLatencyHistogram(payload []byte) (APIServerLatencyHistogram, error) {
	sli := APIServerLatencyHistogram{Buckets: make(map[float64]float64)}
	slo := APIServerLatencyHistogram{Buckets: make(map[float64]float64)}

	parser := textparse.NewPromParser(payload, labels.NewSymbolTable(), false)
	var metricLabels labels.Labels

	for {
		entryType, err := parser.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return APIServerLatencyHistogram{}, fmt.Errorf("failed to parse /metrics payload. Error: %w", err)
		}

		if entryType != textparse.EntrySeries {
			continue
		}

		_, _, value := parser.Series()
		parser.Labels(&metricLabels)

		switch metricLabels.Get(labels.MetricName) {
		case apiServerRequestLatencySLIFamily + "_bucket":
			poolAPIServerLatencyBucket(&sli, metricLabels, value)
		case apiServerRequestLatencySLIFamily + "_count":
			sli.Count += value
		case apiServerRequestLatencySLOFamily + "_bucket":
			poolAPIServerLatencyBucket(&slo, metricLabels, value)
		case apiServerRequestLatencySLOFamily + "_count":
			slo.Count += value
		}
	}

	if len(sli.Buckets) > 0 {
		return sli, nil
	}
	if len(slo.Buckets) > 0 {
		return slo, nil
	}

	return APIServerLatencyHistogram{}, ErrAPIServerRequestLatencyUnsupported
}

func poolAPIServerLatencyBucket(histogram *APIServerLatencyHistogram, metricLabels labels.Labels, value float64) {
	le, err := strconv.ParseFloat(strings.TrimSpace(metricLabels.Get("le")), 64)
	if err != nil {
		return
	}

	histogram.Buckets[le] += value
}
