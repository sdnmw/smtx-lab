package agent

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"
)

func (c Collector) CollectIptables(ctx context.Context, allowlist []string) (CommandSnapshot, error) {
	out, err := c.run(ctx, "iptables-save")
	if err != nil {
		return CommandSnapshot{Available: false}, err
	}
	lines, truncated := boundedLines(out, c.MaxOutputBytes)
	filtered := filterIptablesLines(lines, allowlist)
	return CommandSnapshot{
		Available: true,
		Captured:  true,
		Truncated: truncated,
		Lines:     filtered,
		LineCount: len(filtered),
	}, nil
}

func (c Collector) CollectConntrack(ctx context.Context, filter ConntrackFilter) (CommandSnapshot, error) {
	out, err := c.run(ctx, "conntrack", "-L")
	if err != nil {
		return CommandSnapshot{Available: false}, err
	}
	lines, truncated := boundedLines(out, c.MaxOutputBytes)
	filtered := filterConntrackLines(lines, filter)
	return CommandSnapshot{
		Available: true,
		Captured:  true,
		Truncated: truncated,
		Lines:     filtered,
		LineCount: len(filtered),
	}, nil
}

func (c Collector) CollectRoutes(ctx context.Context) (CommandSnapshot, error) {
	out, err := c.run(ctx, "ip", "-o", "route", "show")
	if err != nil {
		return CommandSnapshot{Available: false}, err
	}
	lines, truncated := boundedLines(out, c.MaxOutputBytes)
	return CommandSnapshot{
		Available: true,
		Captured:  true,
		Truncated: truncated,
		Lines:     lines,
		LineCount: len(lines),
	}, nil
}

func (c Collector) CollectIPVS() (IPVSSnapshot, error) {
	procRoot := c.ProcRoot
	if procRoot == "" {
		procRoot = "/proc"
	}
	ipvsPath := filepath.Join(procRoot, "net", "ip_vs")
	b, err := os.ReadFile(ipvsPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return IPVSSnapshot{Available: false, Enabled: false}, nil
		}
		return IPVSSnapshot{Available: false, Enabled: false}, err
	}
	lines, _ := boundedLines(b, c.MaxOutputBytes)
	snapshot := IPVSSnapshot{
		Available: true,
		Enabled:   len(lines) > 0,
		Lines:     lines,
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "TCP") || strings.HasPrefix(trimmed, "UDP") {
			snapshot.ServiceCount++
		}
		if strings.HasPrefix(trimmed, "->") {
			snapshot.RealServerCount++
		}
	}
	return snapshot, nil
}

func (c Collector) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	timeout := c.CommandTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if cmdCtx.Err() != nil {
		return nil, cmdCtx.Err()
	}
	if err != nil {
		if stderr.Len() > 0 {
			return nil, errors.New(strings.TrimSpace(stderr.String()))
		}
		return nil, err
	}
	return out, nil
}

func boundedLines(data []byte, maxBytes int) ([]string, bool) {
	truncated := false
	if maxBytes > 0 && len(data) > maxBytes {
		data = data[:maxBytes]
		truncated = true
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, truncated
}

func filterIptablesLines(lines []string, allowlist []string) []string {
	if len(allowlist) == 0 {
		return lines
	}
	var filtered []string
	for _, line := range lines {
		chain := iptablesChain(line)
		if chain == "" || chainAllowed(chain, allowlist) {
			filtered = append(filtered, line)
		}
	}
	return filtered
}

func iptablesChain(line string) string {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return ""
	}
	switch fields[0] {
	case ":":
		return fields[1]
	case "-A", "-N":
		return fields[1]
	default:
		if strings.HasPrefix(fields[0], ":") {
			return strings.TrimPrefix(fields[0], ":")
		}
		return ""
	}
}

func chainAllowed(chain string, allowlist []string) bool {
	for _, pattern := range allowlist {
		ok, err := path.Match(pattern, chain)
		if err == nil && ok {
			return true
		}
		if pattern == chain {
			return true
		}
	}
	return false
}

func filterConntrackLines(lines []string, filter ConntrackFilter) []string {
	protocols := map[string]struct{}{}
	for _, protocol := range filter.Protocols {
		protocols[strings.ToLower(protocol)] = struct{}{}
	}
	maxEntries := filter.MaxEntries
	if maxEntries <= 0 {
		maxEntries = 2000
	}
	out := make([]string, 0, min(maxEntries, len(lines)))
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(protocols) > 0 {
			if _, ok := protocols[strings.ToLower(fields[0])]; !ok {
				continue
			}
		}
		out = append(out, line)
		if len(out) >= maxEntries {
			break
		}
	}
	return out
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
