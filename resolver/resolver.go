// Package resolver selects the fastest GDELT server IP via concurrent TCP probing.
package resolver

import (
	"context"
	"fmt"
	"math"
	"net"
	"sync"
	"time"
)

const (
	defaultBaseURL = "http://data.gdeltproject.org/gdeltv2"
	defaultHost    = "data.gdeltproject.org"
	gdeltHost      = "data.gdeltproject.org"
	probeTimeout   = 3 * time.Second
)

// Result holds the chosen endpoint info.
type Result struct {
	BaseURL    string // e.g. "http://1.2.3.4/gdeltv2"
	HostHeader string // set to gdeltHost when using IP-direct, empty otherwise
}

var (
	cachedResult *Result
	cacheOnce    sync.Once
)

// ResolveFastestBaseURL probes all IPs for gdeltHost and returns the fastest.
// The result is cached for the process lifetime.
func ResolveFastestBaseURL(ctx context.Context) (*Result, error) {
	var resolveErr error
	cacheOnce.Do(func() {
		result, err := resolve(ctx)
		if err != nil {
			resolveErr = err
			return
		}
		cachedResult = result
	})
	if resolveErr != nil {
		return nil, resolveErr
	}
	return cachedResult, nil
}

// resolve performs the actual DNS resolution and TCP probing.
func resolve(ctx context.Context) (*Result, error) {
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip4", gdeltHost)
	if err != nil {
		return nil, fmt.Errorf("dns lookup %s: %w", gdeltHost, err)
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("no IPv4 addresses found for %s", gdeltHost)
	}

	type probeResult struct {
		addr  string
		dur   time.Duration
		index int
	}

	results := make(chan probeResult, len(addrs))
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	for i, ip := range addrs {
		i := i
		addr := ip.String()
		go func() {
			start := time.Now()
			dialer := net.Dialer{Timeout: probeTimeout}
			conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(addr, "80"))
			if err != nil {
				return // probe failed, skip
			}
			conn.Close()
			dur := time.Since(start)
			results <- probeResult{addr: addr, dur: dur, index: i}
		}()
	}

	var fastest *probeResult
	received := 0
	timer := time.NewTimer(probeTimeout)
	defer timer.Stop()

	for received < len(addrs) {
		select {
		case pr := <-results:
			received++
			if fastest == nil || pr.dur < fastest.dur {
				fastest = &pr
			}
		case <-timer.C:
			// timeout — take what we have
			goto done
		case <-ctx.Done():
			goto done
		}
	}
done:

	if fastest == nil {
		// Fallback to direct hostname
		return &Result{
			BaseURL:    defaultBaseURL,
			HostHeader: "",
		}, nil
	}

	return &Result{
		BaseURL:    fmt.Sprintf("http://%s/gdeltv2", fastest.addr),
		HostHeader: gdeltHost,
	}, nil
}

// MinLatency returns the minimum duration from a probe set (used for metrics).
func MinLatency(probes []time.Duration) time.Duration {
	if len(probes) == 0 {
		return 0
	}
	min := probes[0]
	for _, d := range probes[1:] {
		if d < min {
			min = d
		}
	}
	return min
}

// MaxLatency returns the maximum duration from a probe set.
func MaxLatency(probes []time.Duration) time.Duration {
	if len(probes) == 0 {
		return 0
	}
	max := probes[0]
	for _, d := range probes[1:] {
		if d > max {
			max = d
		}
	}
	return max
}

// AvgLatency returns the average duration from a probe set.
func AvgLatency(probes []time.Duration) time.Duration {
	if len(probes) == 0 {
		return 0
	}
	var total time.Duration
	for _, d := range probes {
		total += d
	}
	return total / time.Duration(len(probes))
}

// StdDevLatency returns the standard deviation of probe durations.
func StdDevLatency(probes []time.Duration) time.Duration {
	if len(probes) < 2 {
		return 0
	}
	avg := AvgLatency(probes)
	var sumSquares float64
	for _, d := range probes {
		diff := float64(d - avg)
		sumSquares += diff * diff
	}
	variance := sumSquares / float64(len(probes))
	return time.Duration(math.Sqrt(variance))
}
