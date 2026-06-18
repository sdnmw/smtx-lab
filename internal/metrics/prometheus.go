package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type PrometheusClient struct {
	BaseURL    string
	HTTPClient *http.Client
}

type Series struct {
	Metric map[string]string
	Values []Sample
}

type Sample struct {
	Timestamp time.Time
	Value     float64
}

type queryRangeResponse struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Values []promSample      `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

type promSample [2]any

func (c PrometheusClient) QueryRange(ctx context.Context, query string, start, end time.Time, step time.Duration) ([]Series, error) {
	if c.BaseURL == "" {
		return nil, fmt.Errorf("prometheus base URL is empty")
	}
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	u, err := url.Parse(c.BaseURL)
	if err != nil {
		return nil, err
	}
	u.Path = joinURLPath(u.Path, "/api/v1/query_range")
	q := u.Query()
	q.Set("query", query)
	q.Set("start", strconv.FormatInt(start.Unix(), 10))
	q.Set("end", strconv.FormatInt(end.Unix(), 10))
	q.Set("step", strconv.FormatFloat(step.Seconds(), 'f', -1, 64))
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("prometheus query_range returned HTTP %d", resp.StatusCode)
	}

	var decoded queryRangeResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, err
	}
	if decoded.Status != "success" {
		return nil, fmt.Errorf("prometheus query_range failed: %s", decoded.Error)
	}

	result := make([]Series, 0, len(decoded.Data.Result))
	for _, item := range decoded.Data.Result {
		series := Series{
			Metric: item.Metric,
			Values: make([]Sample, 0, len(item.Values)),
		}
		for _, value := range item.Values {
			sample, err := parsePromSample(value)
			if err != nil {
				return nil, err
			}
			series.Values = append(series.Values, sample)
		}
		result = append(result, series)
	}
	return result, nil
}

func parsePromSample(value promSample) (Sample, error) {
	ts, ok := value[0].(float64)
	if !ok {
		return Sample{}, fmt.Errorf("invalid prometheus timestamp")
	}
	raw, ok := value[1].(string)
	if !ok {
		return Sample{}, fmt.Errorf("invalid prometheus sample value")
	}
	parsed, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return Sample{}, err
	}
	return Sample{
		Timestamp: time.Unix(int64(ts), 0).UTC(),
		Value:     parsed,
	}, nil
}

func joinURLPath(base, suffix string) string {
	if base == "" || base == "/" {
		return suffix
	}
	if base[len(base)-1] == '/' {
		base = base[:len(base)-1]
	}
	return base + suffix
}

const (
	ContainerCPUUsageQuery         = `sum by (namespace,pod,container) (rate(container_cpu_usage_seconds_total{container!="",image!=""}[5m]))`
	ContainerMemoryWorkingSetQuery = `container_memory_working_set_bytes{container!="",image!=""}`
	ContainerCPURequestsQuery      = `kube_pod_container_resource_requests{resource="cpu"}`
	ContainerMemoryRequestsQuery   = `kube_pod_container_resource_requests{resource="memory"}`
	ContainerCPULimitsQuery        = `kube_pod_container_resource_limits{resource="cpu"}`
	ContainerMemoryLimitsQuery     = `kube_pod_container_resource_limits{resource="memory"}`
)
