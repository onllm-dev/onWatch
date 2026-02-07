// perf-monitor - RAM and performance monitoring tool for SynTrack
// Usage: go run main.go [port] [duration]
// Default: port=9211, duration=1m
package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MemorySample represents a single memory measurement
type MemorySample struct {
	Timestamp time.Time `json:"timestamp"`
	RSSBytes  uint64    `json:"rss_bytes"`
	VMSBytes  uint64    `json:"vms_bytes"`
	Phase     string    `json:"phase"` // "idle" or "load"
}

// PhaseStats holds statistics for a monitoring phase
type PhaseStats struct {
	Phase    string  `json:"phase"`
	Samples  int     `json:"samples"`
	MinRSSMB float64 `json:"min_rss_mb"`
	MaxRSSMB float64 `json:"max_rss_mb"`
	AvgRSSMB float64 `json:"avg_rss_mb"`
	P95RSSMB float64 `json:"p95_rss_mb"`
}

// RequestMetric tracks endpoint performance
type RequestMetric struct {
	Endpoint string        `json:"endpoint"`
	Count    int           `json:"count"`
	AvgTime  time.Duration `json:"avg_time"`
	MinTime  time.Duration `json:"min_time"`
	MaxTime  time.Duration `json:"max_time"`
}

// Report contains monitoring results
type Report struct {
	Timestamp      time.Time       `json:"timestamp"`
	PID            int             `json:"pid"`
	Port           int             `json:"port"`
	IdleDuration   time.Duration   `json:"idle_duration"`
	LoadDuration   time.Duration   `json:"load_duration"`
	IdleStats      PhaseStats      `json:"idle_stats"`
	LoadStats      PhaseStats      `json:"load_stats"`
	RequestMetrics []RequestMetric `json:"request_metrics"`
	DeltaRSSMB     float64         `json:"delta_rss_mb"`
	DeltaPercent   float64         `json:"delta_percent"`
	Recommendation string          `json:"recommendation"`
}

var (
	samples      []MemorySample
	samplesMutex sync.RWMutex
	stopMonitor  = make(chan struct{})
)

func main() {
	fmt.Println("╔════════════════════════════════════════════════════════════════╗")
	fmt.Println("║         SynTrack RAM & Performance Monitor                     ║")
	fmt.Println("╚════════════════════════════════════════════════════════════════╝")
	fmt.Println()

	// Parse arguments
	port := 9211
	duration := 1 * time.Minute
	shouldRestart := false

	for i, arg := range os.Args[1:] {
		switch arg {
		case "--restart", "-r":
			shouldRestart = true
		default:
			if i == 0 {
				if p, err := strconv.Atoi(arg); err == nil {
					port = p
				}
			} else if i == 1 {
				if d, err := time.ParseDuration(arg); err == nil {
					duration = d
				}
			}
		}
	}

	var pid int

	if shouldRestart {
		// Stop existing instance
		fmt.Println("🔄 Restart mode enabled")
		stopSynTrack(port)
		time.Sleep(500 * time.Millisecond)

		// Start fresh instance
		pid = startSynTrack(port)
		if pid == 0 {
			fmt.Println("❌ Failed to start SynTrack")
			os.Exit(1)
		}
		fmt.Printf("✓ Started fresh SynTrack instance (PID: %d)\n\n", pid)
	} else {
		// Find existing process
		pid = findSynTrackProcess(port)
		if pid == 0 {
			fmt.Printf("❌ SynTrack process not found on port %d\n", port)
			fmt.Println("\nMake sure SynTrack is running first:")
			fmt.Println("   ./syntrack --debug")
			fmt.Println("\nOr use --restart flag:")
			fmt.Println("   go run main.go --restart")
			os.Exit(1)
		}
		fmt.Printf("✓ Found SynTrack process (PID: %d) on port %d\n", pid, port)
	}

	fmt.Printf("✓ Total monitoring duration: %s\n", duration)
	fmt.Printf("  (Idle: %s, Load: %s)\n\n", duration/2, duration/2)

	// Run monitoring
	report := runMonitoring(pid, port, duration)

	// Display results
	displayResults(report)

	// Save report
	saveReport(report)
}

func runMonitoring(pid, port int, totalDuration time.Duration) *Report {
	halfDuration := totalDuration / 2
	baseURL := fmt.Sprintf("http://localhost:%d", port)

	report := &Report{
		Timestamp: time.Now(),
		PID:       pid,
		Port:      port,
	}

	// === PHASE 1: IDLE MONITORING ===
	fmt.Println("📊 PHASE 1: Monitoring IDLE state (no requests)")
	fmt.Println("   Sampling memory every 5 seconds...")

	stopMonitor = make(chan struct{})
	go sampleMemory(pid, "idle")

	time.Sleep(halfDuration)
	close(stopMonitor)
	time.Sleep(100 * time.Millisecond)

	// Collect idle samples
	samplesMutex.RLock()
	idleSamples := make([]MemorySample, 0, len(samples))
	for _, s := range samples {
		if s.Phase == "idle" {
			idleSamples = append(idleSamples, s)
		}
	}
	samplesMutex.RUnlock()

	report.IdleDuration = halfDuration
	report.IdleStats = calculateStats("idle", idleSamples)

	fmt.Printf("   ✓ Collected %d samples\n", len(idleSamples))
	fmt.Printf("   ✓ Average RSS: %.1f MB\n\n", report.IdleStats.AvgRSSMB)

	// Small pause between phases
	time.Sleep(2 * time.Second)

	// === PHASE 2: LOAD MONITORING ===
	fmt.Println("📊 PHASE 2: Monitoring under LOAD (dashboard requests)")
	fmt.Println("   Making requests to all endpoints...")

	samplesMutex.Lock()
	samples = []MemorySample{} // Reset samples
	samplesMutex.Unlock()

	stopMonitor = make(chan struct{})
	go sampleMemory(pid, "load")

	// Generate load
	requestMetrics := generateLoad(baseURL, halfDuration)

	close(stopMonitor)
	time.Sleep(100 * time.Millisecond)

	// Collect load samples
	samplesMutex.RLock()
	loadSamples := make([]MemorySample, 0, len(samples))
	for _, s := range samples {
		if s.Phase == "load" {
			loadSamples = append(loadSamples, s)
		}
	}
	samplesMutex.RUnlock()

	report.LoadDuration = halfDuration
	report.LoadStats = calculateStats("load", loadSamples)
	report.RequestMetrics = requestMetrics

	fmt.Printf("   ✓ Collected %d samples\n", len(loadSamples))
	fmt.Printf("   ✓ Average RSS: %.1f MB\n\n", report.LoadStats.AvgRSSMB)

	// Calculate delta
	report.DeltaRSSMB = report.LoadStats.AvgRSSMB - report.IdleStats.AvgRSSMB
	if report.IdleStats.AvgRSSMB > 0 {
		report.DeltaPercent = (report.DeltaRSSMB / report.IdleStats.AvgRSSMB) * 100
	}

	// Generate recommendation
	report.Recommendation = generateRecommendation(report)

	return report
}

func findSynTrackProcess(port int) int {
	// Try PID file
	pidFile := filepath.Join(os.Getenv("HOME"), "Library", "Application Support", "syntrack", "syntrack.pid")
	if runtime.GOOS != "darwin" {
		pidFile = filepath.Join(os.Getenv("HOME"), ".local", "share", "syntrack", "syntrack.pid")
	}

	if data, err := os.ReadFile(pidFile); err == nil {
		if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 {
			if isProcessRunning(pid) {
				return pid
			}
		}
	}

	// Fallback: lsof
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		out, err := exec.Command("lsof", "-ti", fmt.Sprintf(":%d", port)).Output()
		if err == nil {
			for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				if pid, err := strconv.Atoi(strings.TrimSpace(line)); err == nil && pid > 0 {
					if isSyntrackProcess(pid) {
						return pid
					}
				}
			}
		}
	}

	return 0
}

func isProcessRunning(pid int) bool {
	proc, _ := os.FindProcess(pid)
	if proc == nil {
		return false
	}
	// Signal 0 check
	return proc.Signal(os.Signal(nil)) == nil
}

func isSyntrackProcess(pid int) bool {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(out)), "syntrack")
}

func stopSynTrack(port int) {
	// Try PID file first
	pidFile := filepath.Join(os.Getenv("HOME"), "Library", "Application Support", "syntrack", "syntrack.pid")
	if runtime.GOOS != "darwin" {
		pidFile = filepath.Join(os.Getenv("HOME"), ".local", "share", "syntrack", "syntrack.pid")
	}

	if data, err := os.ReadFile(pidFile); err == nil {
		if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 {
			if proc, err := os.FindProcess(pid); err == nil {
				proc.Signal(os.Interrupt)
				fmt.Printf("   Stopped process (PID: %d) via PID file\n", pid)
				time.Sleep(500 * time.Millisecond)
			}
		}
		os.Remove(pidFile)
	}

	// Fallback: kill processes on port
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		out, err := exec.Command("lsof", "-ti", fmt.Sprintf(":%d", port)).Output()
		if err == nil {
			for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				if pid, err := strconv.Atoi(strings.TrimSpace(line)); err == nil && pid > 0 {
					if isSyntrackProcess(pid) {
						if proc, err := os.FindProcess(pid); err == nil {
							proc.Signal(os.Interrupt)
							fmt.Printf("   Stopped process (PID: %d) on port %d\n", pid, port)
						}
					}
				}
			}
		}
	}
}

func startSynTrack(port int) int {
	// Find syntrack binary in various locations
	possiblePaths := []string{
		"./syntrack",
		"../syntrack",
		"../../syntrack",
		"/Users/prakersh/projects/syntrack/syntrack",
	}

	binaryPath := ""
	for _, path := range possiblePaths {
		if _, err := os.Stat(path); err == nil {
			binaryPath = path
			break
		}
	}

	if binaryPath == "" {
		// Try PATH
		binaryPath = "syntrack"
	}

	// Change to the binary's directory so it can find .env and database
	binaryDir := filepath.Dir(binaryPath)
	if binaryDir != "." && binaryDir != "" {
		os.Chdir(binaryDir)
		binaryPath = "./syntrack"
	}

	// Start syntrack in debug mode
	cmd := exec.Command(binaryPath, "--debug", "--port", strconv.Itoa(port))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()

	if err := cmd.Start(); err != nil {
		fmt.Printf("   Error starting SynTrack: %v\n", err)
		return 0
	}

	pid := cmd.Process.Pid

	// Wait for it to be ready
	fmt.Println("   Waiting for SynTrack to be ready...")
	for i := 0; i < 30; i++ {
		time.Sleep(200 * time.Millisecond)

		// Check if process is still running
		if !isProcessRunning(pid) {
			fmt.Println("   ❌ SynTrack process died during startup")
			return 0
		}

		// Check if port is listening
		if conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", port), 100*time.Millisecond); err == nil {
			conn.Close()
			return pid
		}
	}

	fmt.Println("   ⚠️  Timeout waiting for SynTrack to start")
	return 0
}

func sampleMemory(pid int, phase string) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-stopMonitor:
			return
		case <-ticker.C:
			rss, vms := getProcessMemory(pid)
			samplesMutex.Lock()
			samples = append(samples, MemorySample{
				Timestamp: time.Now(),
				RSSBytes:  rss,
				VMSBytes:  vms,
				Phase:     phase,
			})
			samplesMutex.Unlock()
		}
	}
}

func getProcessMemory(pid int) (rss, vms uint64) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return 0, 0
	}

	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "rss=,vsz=").Output()
	if err != nil {
		return 0, 0
	}

	parts := strings.Fields(string(out))
	if len(parts) >= 2 {
		rssKB, _ := strconv.ParseUint(parts[0], 10, 64)
		vmsKB, _ := strconv.ParseUint(parts[1], 10, 64)
		return rssKB * 1024, vmsKB * 1024
	}

	return 0, 0
}

func generateLoad(baseURL string, duration time.Duration) []RequestMetric {
	endpoints := []string{
		"/",
		"/api/providers",
		"/api/current",
		"/api/history?range=6h",
		"/api/cycles",
		"/api/summary",
		"/api/sessions",
		"/api/insights",
	}

	client := &http.Client{Timeout: 10 * time.Second}
	metrics := make(map[string]*RequestMetric)
	start := time.Now()
	count := 0

	for time.Since(start) < duration {
		for _, endpoint := range endpoints {
			url := baseURL + endpoint
			reqStart := time.Now()
			resp, err := client.Get(url)
			reqDuration := time.Since(reqStart)

			if err == nil {
				resp.Body.Close()
			}

			if _, ok := metrics[endpoint]; !ok {
				metrics[endpoint] = &RequestMetric{
					Endpoint: endpoint,
					MinTime:  reqDuration,
				}
			}

			m := metrics[endpoint]
			m.Count++
			m.AvgTime = (m.AvgTime*time.Duration(m.Count-1) + reqDuration) / time.Duration(m.Count)
			if reqDuration < m.MinTime {
				m.MinTime = reqDuration
			}
			if reqDuration > m.MaxTime {
				m.MaxTime = reqDuration
			}

			count++
			if count%50 == 0 {
				fmt.Printf("   ... made %d requests\n", count)
			}
		}

		time.Sleep(100 * time.Millisecond)
	}

	// Convert to slice
	result := make([]RequestMetric, 0, len(metrics))
	for _, m := range metrics {
		result = append(result, *m)
	}

	fmt.Printf("   ✓ Total requests made: %d\n", count)
	return result
}

func calculateStats(phase string, samples []MemorySample) PhaseStats {
	if len(samples) == 0 {
		return PhaseStats{Phase: phase}
	}

	rssValues := make([]float64, len(samples))
	var totalRSS uint64

	for i, s := range samples {
		rssValues[i] = float64(s.RSSBytes) / (1024 * 1024)
		totalRSS += s.RSSBytes
	}

	sort.Float64s(rssValues)

	minRSS := rssValues[0]
	maxRSS := rssValues[len(rssValues)-1]
	avgRSS := float64(totalRSS) / float64(len(samples)) / (1024 * 1024)

	p95Index := int(float64(len(rssValues)) * 0.95)
	if p95Index >= len(rssValues) {
		p95Index = len(rssValues) - 1
	}
	p95RSS := rssValues[p95Index]

	return PhaseStats{
		Phase:    phase,
		Samples:  len(samples),
		MinRSSMB: minRSS,
		MaxRSSMB: maxRSS,
		AvgRSSMB: avgRSS,
		P95RSSMB: p95RSS,
	}
}

func generateRecommendation(r *Report) string {
	idleAvg := r.IdleStats.AvgRSSMB
	delta := r.DeltaRSSMB

	if idleAvg > 50 {
		return fmt.Sprintf("⚠️  High idle memory (%.1f MB). Consider optimizing.", idleAvg)
	}

	if delta > 10 {
		return fmt.Sprintf("⚠️  Large memory increase under load (+%.1f MB). Monitor closely.", delta)
	}

	if delta < 1 {
		return fmt.Sprintf("✅ Excellent! Minimal memory overhead (+%.1f MB) under load.", delta)
	}

	return fmt.Sprintf("✅ Good performance. Memory overhead is acceptable (+%.1f MB).", delta)
}

func displayResults(r *Report) {
	fmt.Println()
	fmt.Println("╔════════════════════════════════════════════════════════════════╗")
	fmt.Println("║                    MONITORING RESULTS                          ║")
	fmt.Println("╚════════════════════════════════════════════════════════════════╝")
	fmt.Println()

	fmt.Println("┌────────────────────────────────────────────────────────────────┐")
	fmt.Println("│ IDLE STATE (Agent running, no requests)                        │")
	fmt.Println("├────────────────────────────────────────────────────────────────┤")
	fmt.Printf("│  Samples:    %-49d │\n", r.IdleStats.Samples)
	fmt.Printf("│  Min RSS:    %-10.1f MB                                     │\n", r.IdleStats.MinRSSMB)
	fmt.Printf("│  Max RSS:    %-10.1f MB                                     │\n", r.IdleStats.MaxRSSMB)
	fmt.Printf("│  Avg RSS:    %-10.1f MB                                     │\n", r.IdleStats.AvgRSSMB)
	fmt.Printf("│  P95 RSS:    %-10.1f MB                                     │\n", r.IdleStats.P95RSSMB)
	fmt.Println("└────────────────────────────────────────────────────────────────┘")

	fmt.Println()
	fmt.Println("┌────────────────────────────────────────────────────────────────┐")
	fmt.Println("│ LOAD STATE (Dashboard requests active)                         │")
	fmt.Println("├────────────────────────────────────────────────────────────────┤")
	fmt.Printf("│  Samples:    %-49d │\n", r.LoadStats.Samples)
	fmt.Printf("│  Min RSS:    %-10.1f MB                                     │\n", r.LoadStats.MinRSSMB)
	fmt.Printf("│  Max RSS:    %-10.1f MB                                     │\n", r.LoadStats.MaxRSSMB)
	fmt.Printf("│  Avg RSS:    %-10.1f MB                                     │\n", r.LoadStats.AvgRSSMB)
	fmt.Printf("│  P95 RSS:    %-10.1f MB                                     │\n", r.LoadStats.P95RSSMB)
	fmt.Println("└────────────────────────────────────────────────────────────────┘")

	fmt.Println()
	fmt.Println("┌────────────────────────────────────────────────────────────────┐")
	fmt.Println("│ COMPARISON                                                     │")
	fmt.Println("├────────────────────────────────────────────────────────────────┤")
	fmt.Printf("│  Delta (Load - Idle):  %+.1f MB (%+.1f%%)                     │\n", r.DeltaRSSMB, r.DeltaPercent)
	fmt.Println("└────────────────────────────────────────────────────────────────┘")

	fmt.Println()
	fmt.Println("┌────────────────────────────────────────────────────────────────┐")
	fmt.Println("│ HTTP REQUEST PERFORMANCE                                       │")
	fmt.Println("├────────────────────────────────────────────────────────────────┤")
	for _, m := range r.RequestMetrics {
		name := m.Endpoint
		if len(name) > 25 {
			name = name[:22] + "..."
		}
		fmt.Printf("│  %-25s %3d reqs  avg: %6.2fms │\n", name, m.Count, float64(m.AvgTime.Microseconds())/1000)
	}
	fmt.Println("└────────────────────────────────────────────────────────────────┘")

	fmt.Println()
	fmt.Println("┌────────────────────────────────────────────────────────────────┐")
	fmt.Printf("│ %-62s │\n", r.Recommendation)
	fmt.Println("└────────────────────────────────────────────────────────────────┘")
	fmt.Println()
}

func saveReport(r *Report) {
	filename := fmt.Sprintf("perf-report-%s.json", r.Timestamp.Format("20060102-150405"))
	data, _ := json.MarshalIndent(r, "", "  ")
	os.WriteFile(filename, data, 0644)
	fmt.Printf("📄 Full report saved to: %s\n", filename)
}
