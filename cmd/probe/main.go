package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/smtx-lab/smtx-lab-operator/internal/probe"
)

func main() {
	var checks []probe.Check
	if err := json.Unmarshal([]byte(os.Getenv("SMTX_PROBE_CHECKS")), &checks); err != nil {
		fatal(err)
	}
	timeout, err := time.ParseDuration(defaultString(os.Getenv("SMTX_PROBE_TIMEOUT"), "5s"))
	if err != nil {
		fatal(err)
	}
	count, err := strconv.Atoi(defaultString(os.Getenv("SMTX_PROBE_COUNT"), "1"))
	if err != nil {
		fatal(err)
	}
	if count <= 0 {
		count = 1
	}

	sourceNode := os.Getenv("SMTX_SOURCE_NODE")
	report := probe.Report{SourceNode: sourceNode}
	for _, check := range checks {
		report.Results = append(report.Results, runCheck(check, count, timeout))
	}

	out, err := json.Marshal(report)
	if err != nil {
		fatal(err)
	}
	fmt.Println(string(out))
	_ = os.WriteFile("/dev/termination-log", out, 0o644)
}

func runCheck(check probe.Check, count int, timeout time.Duration) probe.Result {
	result := probe.Result{
		CheckID:       check.ID,
		SourceNode:    check.SourceNode,
		TargetPod:     check.TargetPod,
		TargetNode:    check.TargetNode,
		TargetService: check.TargetService,
		Protocol:      strings.ToUpper(check.Protocol),
		Address:       check.Address,
		Port:          check.Port,
		Path:          check.Path,
		TargetIP:      check.TargetIP,
		ServiceIP:     check.ServiceIP,
		Success:       true,
	}
	var latencies []float64
	for i := 0; i < count; i++ {
		start := time.Now()
		if err := probeOnce(check, timeout); err != nil {
			result.Success = false
			result.Error = err.Error()
			break
		}
		latencies = append(latencies, float64(time.Since(start).Microseconds())/1000)
	}
	result.LatencyMsP50 = percentile(latencies, 0.50)
	result.LatencyMsP95 = percentile(latencies, 0.95)
	return result
}

func probeOnce(check probe.Check, timeout time.Duration) error {
	target := net.JoinHostPort(check.Address, strconv.Itoa(int(check.Port)))
	switch strings.ToUpper(check.Protocol) {
	case "HTTP", "HTTPS":
		scheme := "http"
		if strings.EqualFold(check.Protocol, "HTTPS") {
			scheme = "https"
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, scheme+"://"+target, nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 500 {
			return fmt.Errorf("HTTP status %d", resp.StatusCode)
		}
		return nil
	case "UDP":
		conn, err := net.DialTimeout("udp", target, timeout)
		if err != nil {
			return err
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(timeout))
		_, err = conn.Write([]byte("smtx-lab-probe"))
		return err
	default:
		conn, err := net.DialTimeout("tcp", target, timeout)
		if err != nil {
			return err
		}
		return conn.Close()
	}
}

func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	copied := append([]float64(nil), values...)
	sort.Float64s(copied)
	if len(copied) == 1 {
		return copied[0]
	}
	rank := p * float64(len(copied)-1)
	lower := int(rank)
	upper := lower
	if float64(lower) != rank {
		upper++
	}
	if upper >= len(copied) {
		upper = len(copied) - 1
	}
	weight := rank - float64(lower)
	return copied[lower]*(1-weight) + copied[upper]*weight
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	_ = os.WriteFile("/dev/termination-log", []byte(err.Error()), 0o644)
	os.Exit(1)
}
