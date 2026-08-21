package main

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

type options struct {
	proxy       string
	downloadMB  int
	uploadMB    int
	parallel    int
	latencyRuns int
	interval    time.Duration
	timeout     time.Duration
}

type sample struct {
	At   time.Duration
	Mbps float64
}

type result struct {
	Name      string
	Bytes     uint64
	Duration  time.Duration
	FirstData time.Duration
	Average   float64
	P10       float64
	P50       float64
	P90       float64
	CV        float64
	ActivePct float64
	Samples   []sample
}

type countingReader struct {
	r io.Reader
	n *atomic.Uint64
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	r.n.Add(uint64(n))
	return n, err
}

type zeroReader struct {
	remaining int64
}

func (r *zeroReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	for i := range p {
		p[i] = 0
	}
	r.remaining -= int64(len(p))
	return len(p), nil
}

func main() {
	var o options
	flag.StringVar(&o.proxy, "proxy", "127.0.0.1:1080", "SOCKS5 proxy address")
	flag.IntVar(&o.downloadMB, "download-mib", 8, "download size in MiB")
	flag.IntVar(&o.uploadMB, "upload-mib", 8, "upload size in MiB")
	flag.IntVar(&o.parallel, "parallel", 4, "parallel download streams")
	flag.IntVar(&o.latencyRuns, "latency-runs", 5, "fresh HTTPS connection latency samples")
	flag.DurationVar(&o.interval, "interval", 250*time.Millisecond, "sampling interval")
	flag.DurationVar(&o.timeout, "timeout", 2*time.Minute, "per-stage timeout")
	flag.Parse()

	if err := validate(o); err != nil {
		fmt.Fprintln(os.Stderr, "wsitbench:", err)
		os.Exit(2)
	}

	fmt.Printf("WSIT isolated benchmark through socks5h://%s\n", o.proxy)
	fmt.Printf("sampling=%s download=%d MiB upload=%d MiB parallel=%d\n\n", o.interval, o.downloadMB, o.uploadMB, o.parallel)
	if o.latencyRuns > 0 {
		values, err := measureLatency(o.proxy, o.latencyRuns, o.timeout)
		if err != nil {
			fmt.Fprintln(os.Stderr, "latency failed:", err)
			os.Exit(1)
		}
		printLatency("fresh HTTPS", values)
		values, err = measureWarmLatency(o.proxy, o.latencyRuns, o.timeout)
		if err != nil {
			fmt.Fprintln(os.Stderr, "warm latency failed:", err)
			os.Exit(1)
		}
		printLatency("warm HTTPS", values)
	}

	downloadURL := fmt.Sprintf("https://speed.cloudflare.com/__down?bytes=%d", int64(o.downloadMB)*1024*1024)
	uploadURL := "https://speed.cloudflare.com/__up"

	stages := []struct {
		name string
		run  func(context.Context, *http.Client, *atomic.Uint64) error
	}{
		{
			name: "download",
			run: func(ctx context.Context, client *http.Client, counter *atomic.Uint64) error {
				return download(ctx, client, downloadURL, counter)
			},
		},
		{
			name: "upload",
			run: func(ctx context.Context, client *http.Client, counter *atomic.Uint64) error {
				return upload(ctx, client, uploadURL, int64(o.uploadMB)*1024*1024, counter)
			},
		},
	}
	if o.parallel > 1 {
		stages = append(stages, struct {
			name string
			run  func(context.Context, *http.Client, *atomic.Uint64) error
		}{
			name: fmt.Sprintf("download-%dx", o.parallel),
			run: func(ctx context.Context, client *http.Client, counter *atomic.Uint64) error {
				return parallelDownload(ctx, client, downloadURL, o.parallel, counter)
			},
		})
	}

	for _, stage := range stages {
		client := newHTTPClient(o.proxy, o.parallel, o.timeout)
		ctx, cancel := context.WithTimeout(context.Background(), o.timeout)
		res, err := measure(ctx, stage.name, o.interval, func(counter *atomic.Uint64) error {
			return stage.run(ctx, client, counter)
		})
		cancel()
		client.CloseIdleConnections()
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s failed: %v\n", stage.name, err)
			os.Exit(1)
		}
		printResult(res)
	}
}

func validate(o options) error {
	if _, _, err := net.SplitHostPort(o.proxy); err != nil {
		return fmt.Errorf("invalid proxy address: %w", err)
	}
	if o.downloadMB < 1 || o.downloadMB > 1024 {
		return errors.New("download-mib must be within 1..1024")
	}
	if o.uploadMB < 1 || o.uploadMB > 1024 {
		return errors.New("upload-mib must be within 1..1024")
	}
	if o.parallel < 1 || o.parallel > 32 {
		return errors.New("parallel must be within 1..32")
	}
	if o.latencyRuns < 0 || o.latencyRuns > 20 {
		return errors.New("latency-runs must be within 0..20")
	}
	if o.interval < 50*time.Millisecond || o.interval > 5*time.Second {
		return errors.New("interval must be within 50ms..5s")
	}
	return nil
}

func measureLatency(proxy string, runs int, timeout time.Duration) ([]time.Duration, error) {
	values := make([]time.Duration, 0, runs)
	for i := 0; i < runs; i++ {
		client := newHTTPClient(proxy, 1, timeout)
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.cloudflare.com/cdn-cgi/trace", nil)
		if err == nil {
			setBenchmarkHeaders(req)
			started := time.Now()
			var resp *http.Response
			resp, err = client.Do(req)
			if err == nil {
				_, err = io.Copy(io.Discard, resp.Body)
				closeErr := resp.Body.Close()
				if err == nil {
					err = closeErr
				}
				if err == nil && (resp.StatusCode < 200 || resp.StatusCode >= 300) {
					err = fmt.Errorf("HTTP %s", resp.Status)
				}
			}
			if err == nil {
				values = append(values, time.Since(started))
			}
		}
		cancel()
		client.CloseIdleConnections()
		if err != nil {
			return nil, fmt.Errorf("sample %d: %w", i+1, err)
		}
	}
	return values, nil
}

func measureWarmLatency(proxy string, runs int, timeout time.Duration) ([]time.Duration, error) {
	client := newHTTPClient(proxy, 1, timeout)
	defer client.CloseIdleConnections()
	values := make([]time.Duration, 0, runs)
	for i := -1; i < runs; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.cloudflare.com/cdn-cgi/trace", nil)
		if err == nil {
			setBenchmarkHeaders(req)
			started := time.Now()
			var resp *http.Response
			resp, err = client.Do(req)
			if err == nil {
				_, err = io.Copy(io.Discard, resp.Body)
				closeErr := resp.Body.Close()
				if err == nil {
					err = closeErr
				}
				if err == nil && (resp.StatusCode < 200 || resp.StatusCode >= 300) {
					err = fmt.Errorf("HTTP %s", resp.Status)
				}
			}
			if err == nil && i >= 0 {
				values = append(values, time.Since(started))
			}
		}
		cancel()
		if err != nil {
			return nil, fmt.Errorf("sample %d: %w", i+2, err)
		}
	}
	return values, nil
}

func printLatency(label string, values []time.Duration) {
	if len(values) == 0 {
		return
	}
	sorted := append([]time.Duration(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	p50 := sorted[len(sorted)/2]
	p90 := sorted[int(math.Ceil(float64(len(sorted))*0.9))-1]
	fmt.Printf("%s latency: min=%s p50=%s p90=%s max=%s\n  samples:", label,
		sorted[0].Round(time.Millisecond), p50.Round(time.Millisecond), p90.Round(time.Millisecond), sorted[len(sorted)-1].Round(time.Millisecond))
	for _, value := range values {
		fmt.Printf(" %s", value.Round(time.Millisecond))
	}
	fmt.Println()
	fmt.Println()
}

func newHTTPClient(proxy string, parallel int, timeout time.Duration) *http.Client {
	transport := &http.Transport{
		DialContext:         socks5Dialer(proxy, 15*time.Second),
		DisableCompression:  true,
		ForceAttemptHTTP2:   false,
		MaxIdleConns:        parallel + 2,
		MaxIdleConnsPerHost: parallel + 2,
		MaxConnsPerHost:     parallel + 2,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
	}
	return &http.Client{Transport: transport, Timeout: timeout}
}

func download(ctx context.Context, client *http.Client, url string, counter *atomic.Uint64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	setBenchmarkHeaders(req)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %s", resp.Status)
	}
	_, err = io.Copy(io.Discard, &countingReader{r: resp.Body, n: counter})
	return err
}

func upload(ctx context.Context, client *http.Client, url string, size int64, counter *atomic.Uint64) error {
	body := &countingReader{r: &zeroReader{remaining: size}, n: counter}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, io.NopCloser(body))
	if err != nil {
		return err
	}
	req.ContentLength = size
	setBenchmarkHeaders(req)
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %s", resp.Status)
	}
	return nil
}

func setBenchmarkHeaders(req *http.Request) {
	req.Header.Set("User-Agent", "curl/8.14.1")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Cache-Control", "no-cache")
}

func parallelDownload(ctx context.Context, client *http.Client, url string, parallel int, counter *atomic.Uint64) error {
	var wg sync.WaitGroup
	errCh := make(chan error, parallel)
	for i := 0; i < parallel; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := download(ctx, client, url, counter); err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			return err
		}
	}
	return nil
}

func measure(ctx context.Context, name string, interval time.Duration, run func(*atomic.Uint64) error) (result, error) {
	var counter atomic.Uint64
	done := make(chan error, 1)
	started := time.Now()
	go func() { done <- run(&counter) }()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	lastAt := started
	var lastBytes uint64
	var samples []sample
	appendSample := func(now time.Time) {
		elapsed := now.Sub(lastAt)
		if elapsed <= 0 {
			return
		}
		current := counter.Load()
		delta := current - lastBytes
		samples = append(samples, sample{
			At:   now.Sub(started),
			Mbps: float64(delta) * 8 / elapsed.Seconds() / 1_000_000,
		})
		lastBytes = current
		lastAt = now
	}

	for {
		select {
		case <-ctx.Done():
			return result{}, ctx.Err()
		case now := <-ticker.C:
			appendSample(now)
		case err := <-done:
			finished := time.Now()
			if finished.Sub(lastAt) >= interval/5 {
				appendSample(finished)
			}
			if err != nil {
				return result{}, err
			}
			return summarize(name, counter.Load(), finished.Sub(started), samples), nil
		}
	}
}

func summarize(name string, bytes uint64, duration time.Duration, samples []sample) result {
	res := result{
		Name:     name,
		Bytes:    bytes,
		Duration: duration,
		Average:  float64(bytes) * 8 / duration.Seconds() / 1_000_000,
		Samples:  samples,
	}
	if len(samples) == 0 {
		return res
	}
	values := make([]float64, len(samples))
	var sum float64
	for i, s := range samples {
		values[i] = s.Mbps
		sum += s.Mbps
		if res.FirstData == 0 && s.Mbps > 0 {
			res.FirstData = s.At
		}
		if s.Mbps >= res.Average*0.10 {
			res.ActivePct++
		}
	}
	res.ActivePct = res.ActivePct * 100 / float64(len(samples))
	mean := sum / float64(len(values))
	var variance float64
	for _, v := range values {
		d := v - mean
		variance += d * d
	}
	if mean > 0 {
		res.CV = math.Sqrt(variance/float64(len(values))) / mean
	}
	sort.Float64s(values)
	res.P10 = percentile(values, 0.10)
	res.P50 = percentile(values, 0.50)
	res.P90 = percentile(values, 0.90)
	return res
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	position := p * float64(len(sorted)-1)
	lo := int(math.Floor(position))
	hi := int(math.Ceil(position))
	if lo == hi {
		return sorted[lo]
	}
	return sorted[lo] + (sorted[hi]-sorted[lo])*(position-float64(lo))
}

func printResult(res result) {
	fmt.Printf("%-12s %6.2f MiB in %6.3fs  avg=%6.2f Mbps  first=%5.2fs  p10/p50/p90=%5.2f/%5.2f/%5.2f  active=%5.1f%%  cv=%.2f\n",
		res.Name, float64(res.Bytes)/(1024*1024), res.Duration.Seconds(), res.Average, res.FirstData.Seconds(),
		res.P10, res.P50, res.P90, res.ActivePct, res.CV)
	fmt.Print("  timeline: ")
	for i, s := range res.Samples {
		if i > 0 {
			fmt.Print(" ")
		}
		fmt.Printf("%.1f", s.Mbps)
	}
	fmt.Println()
	fmt.Println()
}

func socks5Dialer(proxy string, timeout time.Duration) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		dialer := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
		conn, err := dialer.DialContext(ctx, "tcp", proxy)
		if err != nil {
			return nil, fmt.Errorf("dial SOCKS5 %s: %w", proxy, err)
		}
		failed := true
		defer func() {
			if failed {
				_ = conn.Close()
			}
		}()
		deadline := time.Now().Add(timeout)
		if value, ok := ctx.Deadline(); ok && value.Before(deadline) {
			deadline = value
		}
		if err := conn.SetDeadline(deadline); err != nil {
			return nil, err
		}
		if _, err := conn.Write([]byte{5, 1, 0}); err != nil {
			return nil, fmt.Errorf("SOCKS5 greeting: %w", err)
		}
		var greeting [2]byte
		if _, err := io.ReadFull(conn, greeting[:]); err != nil {
			return nil, fmt.Errorf("SOCKS5 greeting response: %w", err)
		}
		if greeting != [2]byte{5, 0} {
			return nil, fmt.Errorf("SOCKS5 authentication method response %v", greeting)
		}
		host, portText, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("target address %q: %w", address, err)
		}
		port, err := strconv.ParseUint(portText, 10, 16)
		if err != nil {
			return nil, fmt.Errorf("target port %q: %w", portText, err)
		}
		request := []byte{5, 1, 0}
		ip := net.ParseIP(host)
		switch {
		case ip == nil:
			if len(host) == 0 || len(host) > 255 {
				return nil, errors.New("SOCKS5 target hostname length is invalid")
			}
			request = append(request, 3, byte(len(host)))
			request = append(request, host...)
		case ip.To4() != nil:
			request = append(request, 1)
			request = append(request, ip.To4()...)
		default:
			request = append(request, 4)
			request = append(request, ip.To16()...)
		}
		request = binary.BigEndian.AppendUint16(request, uint16(port))
		if _, err := conn.Write(request); err != nil {
			return nil, fmt.Errorf("SOCKS5 CONNECT: %w", err)
		}
		var reply [4]byte
		if _, err := io.ReadFull(conn, reply[:]); err != nil {
			return nil, fmt.Errorf("SOCKS5 CONNECT response: %w", err)
		}
		if reply[0] != 5 || reply[1] != 0 {
			return nil, fmt.Errorf("SOCKS5 CONNECT rejected with code %d", reply[1])
		}
		addressBytes := 0
		switch reply[3] {
		case 1:
			addressBytes = net.IPv4len
		case 4:
			addressBytes = net.IPv6len
		case 3:
			var length [1]byte
			if _, err := io.ReadFull(conn, length[:]); err != nil {
				return nil, err
			}
			addressBytes = int(length[0])
		default:
			return nil, fmt.Errorf("SOCKS5 response address type %d", reply[3])
		}
		if _, err := io.CopyN(io.Discard, conn, int64(addressBytes+2)); err != nil {
			return nil, fmt.Errorf("SOCKS5 response address: %w", err)
		}
		if err := conn.SetDeadline(time.Time{}); err != nil {
			return nil, err
		}
		failed = false
		return conn, nil
	}
}
