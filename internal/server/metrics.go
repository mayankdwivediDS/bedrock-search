package server

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
)

// Metrics collects per-route request counters and latency — and nothing
// else. There are deliberately no Go runtime / GC / process-memory metrics:
// those say nothing about how this API is actually behaving. Everything here
// is keyed by the matched route (e.g. "GET /suggest"), so each number maps to
// a real endpoint.
type Metrics struct {
	mu     sync.Mutex
	routes map[string]*routeStat
	start  time.Time
}

// maxSamples bounds the per-route latency ring buffer used for percentiles.
const maxSamples = 4096

type routeStat struct {
	count   uint64
	errors  uint64 // responses with status >= 400
	sumMs   float64
	minMs   float64
	maxMs   float64
	status  map[int]uint64
	samples []float64 // ring buffer of recent latencies (ms)
	pos     int
	filled  bool
}

// NewMetrics returns an empty collector with the clock started.
func NewMetrics() *Metrics {
	return &Metrics{routes: make(map[string]*routeStat), start: time.Now()}
}

// record folds one completed request into its route's stats.
func (m *Metrics) record(key string, status int, ms float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.routes[key]
	if s == nil {
		s = &routeStat{minMs: ms, maxMs: ms, status: make(map[int]uint64), samples: make([]float64, maxSamples)}
		m.routes[key] = s
	}
	s.count++
	if status >= 400 {
		s.errors++
	}
	s.sumMs += ms
	if ms < s.minMs {
		s.minMs = ms
	}
	if ms > s.maxMs {
		s.maxMs = ms
	}
	s.status[status]++
	s.samples[s.pos] = ms
	s.pos = (s.pos + 1) % maxSamples
	if s.pos == 0 {
		s.filled = true
	}
}

// snapshot renders the current stats as a JSON-ready map.
func (m *Metrics) snapshot() fiber.Map {
	m.mu.Lock()
	defer m.mu.Unlock()

	routes := make(fiber.Map, len(m.routes))
	for key, s := range m.routes {
		var samples []float64
		if s.filled {
			samples = append(samples, s.samples...)
		} else {
			samples = append(samples, s.samples[:s.pos]...)
		}
		sort.Float64s(samples)
		pct := func(p float64) float64 {
			if len(samples) == 0 {
				return 0
			}
			idx := int(math.Round(p / 100 * float64(len(samples)-1)))
			return samples[idx]
		}
		avg := 0.0
		if s.count > 0 {
			avg = s.sumMs / float64(s.count)
		}
		routes[key] = fiber.Map{
			"requests": s.count,
			"errors":   s.errors,
			"avg_ms":   round3(avg),
			"p50_ms":   round3(pct(50)),
			"p95_ms":   round3(pct(95)),
			"p99_ms":   round3(pct(99)),
			"min_ms":   round3(s.minMs),
			"max_ms":   round3(s.maxMs),
			"status":   s.status,
		}
	}
	return fiber.Map{
		"uptime_seconds": int64(time.Since(m.start).Seconds()),
		"routes":         routes,
	}
}

func round3(f float64) float64 { return math.Round(f*1000) / 1000 }

// prometheus renders the same per-route stats in Prometheus text exposition
// format (v0.0.4). Counters for requests/errors/responses, a summary with
// quantiles for latency, and an uptime gauge. No runtime/GC metrics.
func (m *Metrics) prometheus() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	keys := make([]string, 0, len(m.routes))
	for k := range m.routes {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder

	b.WriteString("# HELP gosuggest_http_requests_total Total HTTP requests per route.\n")
	b.WriteString("# TYPE gosuggest_http_requests_total counter\n")
	for _, k := range keys {
		method, route := splitKey(k)
		fmt.Fprintf(&b, "gosuggest_http_requests_total{method=%q,route=%q} %d\n", method, route, m.routes[k].count)
	}

	b.WriteString("# HELP gosuggest_http_request_errors_total HTTP responses with status >= 400.\n")
	b.WriteString("# TYPE gosuggest_http_request_errors_total counter\n")
	for _, k := range keys {
		method, route := splitKey(k)
		fmt.Fprintf(&b, "gosuggest_http_request_errors_total{method=%q,route=%q} %d\n", method, route, m.routes[k].errors)
	}

	b.WriteString("# HELP gosuggest_http_responses_total HTTP responses per status code.\n")
	b.WriteString("# TYPE gosuggest_http_responses_total counter\n")
	for _, k := range keys {
		method, route := splitKey(k)
		s := m.routes[k]
		codes := make([]int, 0, len(s.status))
		for code := range s.status {
			codes = append(codes, code)
		}
		sort.Ints(codes)
		for _, code := range codes {
			fmt.Fprintf(&b, "gosuggest_http_responses_total{method=%q,route=%q,status=%q} %d\n",
				method, route, strconv.Itoa(code), s.status[code])
		}
	}

	b.WriteString("# HELP gosuggest_http_request_duration_ms Request latency in milliseconds.\n")
	b.WriteString("# TYPE gosuggest_http_request_duration_ms summary\n")
	for _, k := range keys {
		method, route := splitKey(k)
		s := m.routes[k]
		var samples []float64
		if s.filled {
			samples = append(samples, s.samples...)
		} else {
			samples = append(samples, s.samples[:s.pos]...)
		}
		sort.Float64s(samples)
		pct := func(p float64) float64 {
			if len(samples) == 0 {
				return 0
			}
			return samples[int(math.Round(p/100*float64(len(samples)-1)))]
		}
		for _, q := range []struct {
			label string
			p     float64
		}{{"0.5", 50}, {"0.95", 95}, {"0.99", 99}} {
			fmt.Fprintf(&b, "gosuggest_http_request_duration_ms{method=%q,route=%q,quantile=%q} %g\n",
				method, route, q.label, round3(pct(q.p)))
		}
		fmt.Fprintf(&b, "gosuggest_http_request_duration_ms_sum{method=%q,route=%q} %g\n", method, route, round3(s.sumMs))
		fmt.Fprintf(&b, "gosuggest_http_request_duration_ms_count{method=%q,route=%q} %d\n", method, route, s.count)
	}

	b.WriteString("# HELP gosuggest_http_request_duration_ms_max Max observed latency (ms) per route.\n")
	b.WriteString("# TYPE gosuggest_http_request_duration_ms_max gauge\n")
	for _, k := range keys {
		method, route := splitKey(k)
		fmt.Fprintf(&b, "gosuggest_http_request_duration_ms_max{method=%q,route=%q} %g\n", method, route, round3(m.routes[k].maxMs))
	}

	b.WriteString("# HELP gosuggest_uptime_seconds Seconds since the server started.\n")
	b.WriteString("# TYPE gosuggest_uptime_seconds gauge\n")
	fmt.Fprintf(&b, "gosuggest_uptime_seconds %d\n", int64(time.Since(m.start).Seconds()))

	return b.String()
}

// splitKey splits a "METHOD /route" key into its two parts.
func splitKey(key string) (method, route string) {
	parts := strings.SplitN(key, " ", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return key, ""
}

// metricsMiddleware times each request and records it against its matched
// route. The metrics/docs endpoints are skipped so the numbers stay focused
// on the real API surface.
func metricsMiddleware(m *Metrics) fiber.Handler {
	return func(c *fiber.Ctx) error {
		start := time.Now()
		err := c.Next()

		route := c.Route().Path
		// Unmatched paths hit Fiber's catch-all, which reports route "/".
		// Fold them all under one bounded key so (a) random bad URLs can't
		// balloon the metrics map, and (b) they don't pollute "GET /".
		if route == "" || (route == "/" && c.Path() != "/") {
			route = "<not found>"
		}
		if skipMetrics(route) {
			return err
		}
		// When a handler returns an error, Fiber's ErrorHandler sets the
		// response status *after* this middleware unwinds — so read the
		// status off the error instead of c.Response() in that case.
		status := c.Response().StatusCode()
		if err != nil {
			if fe, ok := err.(*fiber.Error); ok {
				status = fe.Code
			} else {
				status = fiber.StatusInternalServerError
			}
		}
		ms := float64(time.Since(start).Microseconds()) / 1000.0
		m.record(c.Method()+" "+route, status, ms)
		return err
	}
}

// skipMetrics excludes observability/docs routes so /metrics reflects only
// the API that matters.
func skipMetrics(route string) bool {
	switch route {
	case "/metrics", "/metrics/json", "/openapi.yaml":
		return true
	}
	return len(route) >= 5 && route[:5] == "/docs"
}

// metricsPromHandler — GET /metrics. Prometheus text exposition format.
func metricsPromHandler(m *Metrics, inst *Instance) fiber.Handler {
	return func(c *fiber.Ctx) error {
		c.Set(fiber.HeaderContentType, "text/plain; version=0.0.4; charset=utf-8")
		return c.SendString(m.prometheus() + corpusPrometheus(gatherLiveStats(inst)))
	}
}

// metricsHandler — GET /metrics/json. Per-route counters + latency + corpus
// sizes, JSON.
func metricsHandler(m *Metrics, inst *Instance) fiber.Handler {
	return func(c *fiber.Ctx) error {
		out := m.snapshot()
		ls := gatherLiveStats(inst)
		out["corpus"] = fiber.Map{
			"corpus_words": ls.CorpusWords,
			"hot_words":    ls.HotWords,
			"hot_entries":  ls.HotEntries,
			"hot_word_cap": ls.HotWordCap,
			"cold_words":   ls.ColdWords,
			"hit_rate":     round3(ls.HitRate),
			"lookups_hit":  ls.LookupsHit,
			"lookups_miss": ls.LookupsMiss,
			"evicted":      ls.Evicted,
		}
		return c.JSON(out)
	}
}

// liveStats is a snapshot of corpus + hot-cache sizes.
type liveStats struct {
	CorpusWords int64
	HotWords    int
	HotEntries  int
	HotWordCap  int
	ColdWords   int64
	HitRate     float64
	LookupsHit  uint64
	LookupsMiss uint64
	Evicted     uint64
}

// gatherLiveStats reads the current corpus size and hot-cache occupancy from
// the live engine. cold = corpus - hot (words that exist only on disk).
func gatherLiveStats(inst *Instance) liveStats {
	e := inst.Engine()
	if e == nil {
		return liveStats{}
	}
	cs := e.Cache().Stats()
	corpusWords := e.Reader().WordCount()
	cold := corpusWords - int64(cs.WordCount)
	if cold < 0 {
		cold = 0
	}
	return liveStats{
		CorpusWords: corpusWords,
		HotWords:    cs.WordCount,
		HotEntries:  cs.Entries,
		HotWordCap:  cs.WordCap,
		ColdWords:   cold,
		HitRate:     cs.HitRate,
		LookupsHit:  cs.LookupsHit,
		LookupsMiss: cs.LookupsMiss,
		Evicted:     cs.TotalEvicted,
	}
}

// corpusPrometheus renders the corpus/hot/cold gauges in Prometheus format.
func corpusPrometheus(s liveStats) string {
	var b strings.Builder
	b.WriteString("# HELP gosuggest_corpus_words Total words in the live corpus (on disk).\n")
	b.WriteString("# TYPE gosuggest_corpus_words gauge\n")
	fmt.Fprintf(&b, "gosuggest_corpus_words %d\n", s.CorpusWords)
	b.WriteString("# HELP gosuggest_hot_words Words currently held in the in-RAM hot cache.\n")
	b.WriteString("# TYPE gosuggest_hot_words gauge\n")
	fmt.Fprintf(&b, "gosuggest_hot_words %d\n", s.HotWords)
	b.WriteString("# HELP gosuggest_cold_words Words only on disk (corpus minus hot).\n")
	b.WriteString("# TYPE gosuggest_cold_words gauge\n")
	fmt.Fprintf(&b, "gosuggest_cold_words %d\n", s.ColdWords)
	b.WriteString("# HELP gosuggest_hot_entries Number of promoted prefixes (tries) in the hot cache.\n")
	b.WriteString("# TYPE gosuggest_hot_entries gauge\n")
	fmt.Fprintf(&b, "gosuggest_hot_entries %d\n", s.HotEntries)
	b.WriteString("# HELP gosuggest_hot_word_cap Max words the hot cache will hold.\n")
	b.WriteString("# TYPE gosuggest_hot_word_cap gauge\n")
	fmt.Fprintf(&b, "gosuggest_hot_word_cap %d\n", s.HotWordCap)
	b.WriteString("# HELP gosuggest_cache_hit_rate Hot-cache hit rate (0..1).\n")
	b.WriteString("# TYPE gosuggest_cache_hit_rate gauge\n")
	fmt.Fprintf(&b, "gosuggest_cache_hit_rate %g\n", round3(s.HitRate))
	b.WriteString("# HELP gosuggest_cache_lookups_total Hot-cache lookups by result.\n")
	b.WriteString("# TYPE gosuggest_cache_lookups_total counter\n")
	fmt.Fprintf(&b, "gosuggest_cache_lookups_total{result=\"hit\"} %d\n", s.LookupsHit)
	fmt.Fprintf(&b, "gosuggest_cache_lookups_total{result=\"miss\"} %d\n", s.LookupsMiss)
	b.WriteString("# HELP gosuggest_hot_evicted_total Words evicted from the hot cache over time.\n")
	b.WriteString("# TYPE gosuggest_hot_evicted_total counter\n")
	fmt.Fprintf(&b, "gosuggest_hot_evicted_total %d\n", s.Evicted)
	return b.String()
}
