package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"time"

	"github.com/Pimeng/gooophira-mp/internal/common/benchmark/benchmetrics"
)

// ---------- 命令行 ----------

type benchConfig struct {
	Clients    int           `json:"clients"`
	Rooms      int           `json:"rooms"`
	Duration   time.Duration `json:"duration"`
	Scenario   string        `json:"scenario"`
	Profile    string        `json:"profile"`
	ProfileDir string        `json:"profile_dir"`
	JSONOut    bool          `json:"json_out"`
	Verbose    bool          `json:"verbose"`
}

func ptr64(v float64) *float64 { return &v }

func parseFlags() benchConfig {
	var (
		clients    = flag.Int("clients", 50, "clients")
		rooms      = flag.Int("rooms", 5, "rooms")
		duration   = flag.Duration("duration", 30*time.Second, "duration")
		scenario   = flag.String("scenario", "room-cycle", "scenario: room-cycle, gameplay, connection-storm, steady-state, mixed")
		profile    = flag.String("profile", "", "pprof: cpu, mem, goroutine, mutex, block, all")
		profileDir = flag.String("profile-dir", "./tmp/profiles", "profile dir")
		jsonOut    = flag.Bool("json", false, "JSON output")
		verbose    = flag.Bool("v", false, "verbose")
		gcPct      = flag.Int("gc", 400, "GOGC percent")
	)
	flag.Parse()
	debug.SetGCPercent(*gcPct)
	return benchConfig{
		Clients: *clients, Rooms: *rooms, Duration: *duration, Scenario: *scenario,
		Profile: *profile, ProfileDir: *profileDir, JSONOut: *jsonOut, Verbose: *verbose,
	}
}

// ---------- 性能分析器 ----------

type profiler struct {
	dir            string
	profiles       []string
	cpuFile        *os.File
	mutexProfiling bool
	blockProfiling bool
}

func startProfiler(cfg benchConfig) (*profiler, error) {
	p := &profiler{dir: cfg.ProfileDir, profiles: make([]string, 0, 8)}
	if err := os.MkdirAll(p.dir, 0755); err != nil {
		return nil, fmt.Errorf("create profile dir: %w", err)
	}
	switch cfg.Profile {
	case "cpu", "all":
		f, err := os.Create(p.dir + "/cpu.pprof")
		if err != nil {
			return nil, err
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			f.Close()
			return nil, err
		}
		p.cpuFile = f
	}
	p.mutexProfiling = cfg.Profile == "mutex" || cfg.Profile == "all"
	p.blockProfiling = cfg.Profile == "block" || cfg.Profile == "all"
	if p.mutexProfiling {
		runtime.SetMutexProfileFraction(1)
	}
	if p.blockProfiling {
		runtime.SetBlockProfileRate(1)
	}
	return p, nil
}

func (p *profiler) stop() {
	if p.cpuFile != nil {
		pprof.StopCPUProfile()
		p.cpuFile.Close()
	}
}

func (p *profiler) writeProfiles(dir string) {
	writeProfile := func(name, path string) string {
		full := dir + "/" + path
		f, err := os.Create(full)
		if err != nil {
			return ""
		}
		defer f.Close()
		if pprof.Lookup(name) != nil {
			_ = pprof.Lookup(name).WriteTo(f, 0)
			return path
		}
		return ""
	}
	profiles := []string{}
	if p.cpuFile != nil {
		profiles = append(profiles, "cpu.pprof")
	}
	if path := writeProfile("goroutine", "goroutine.pprof"); path != "" {
		profiles = append(profiles, path)
	}
	if path := writeProfile("heap", "heap.pprof"); path != "" {
		profiles = append(profiles, path)
	}
	if p.mutexProfiling {
		if path := writeProfile("mutex", "mutex.pprof"); path != "" {
			profiles = append(profiles, path)
		}
	}
	if p.blockProfiling {
		if path := writeProfile("block", "block.pprof"); path != "" {
			profiles = append(profiles, path)
		}
	}
	p.profiles = profiles
}

// ---------- 主程序 ----------

func main() {
	bc := parseFlags()
	out := os.Stdout
	if bc.JSONOut {
		out = os.Stderr
	}

	fmt.Fprintln(out, "Phira MP Server Bench (hub)")
	fmt.Fprintf(out, "  Scenario: %s  Clients: %d  Rooms: %d  Duration: %s\n",
		bc.Scenario, bc.Clients, bc.Rooms, bc.Duration)

	prof, err := startProfiler(bc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "profiler: %v\n", err)
		os.Exit(1)
	}
	defer prof.stop()

	_ = time.Now()
	mc := benchmetrics.NewCollector()

	var results []benchmetrics.BenchResult
	switch bc.Scenario {
	case "mixed":
		results = runMixedScenario(bc, mc)
	default:
		var result benchmetrics.BenchResult
		switch bc.Scenario {
		case "room-cycle":
			result = runRoomCycleScenario(bc, mc)
		case "gameplay":
			result = runGameplayScenario(bc, mc)
		case "connection-storm":
			result = runConnectionStormScenario(bc, mc)
		case "steady-state":
			result = runSteadyStateScenario(bc, mc)
		default:
			fmt.Fprintf(os.Stderr, "unknown scenario: %s\n", bc.Scenario)
			os.Exit(1)
		}
		results = []benchmetrics.BenchResult{result}
	}

	prof.writeProfiles(bc.ProfileDir)

	report := benchmetrics.BenchReport{
		Title:     "Phira MP Server Bench",
		Timestamp: time.Now().Unix(),
		Results:   results,
		Profiles:  prof.profiles,
	}

	if bc.JSONOut {
		enc := json.NewEncoder(os.Stdout)
		_ = enc.Encode(report)
	} else {
		renderer := benchmetrics.NewRenderer(os.Stdout, "Phira MP Server Benchmark")
		renderer.Render(&report)
	}
}
