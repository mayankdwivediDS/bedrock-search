// Package config loads every runtime knob from environment variables (plus
// an optional .env file), validates them, and returns a populated Config.
//
// Every field in Config maps one-to-one to a row in plan.md §12. If you add
// a new tunable, also add a row to that table and a sensible default here.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// Config is the parsed, validated, ready-to-use settings bundle.
type Config struct {
	// 12.1 Server
	Host          string
	Port          int
	LogLevel      string
	LogFile       string
	LogMaxSizeMB  int  // rotate when the active file reaches this size
	LogMaxBackups int  // number of rotated files to keep on disk
	LogMaxAgeDays int  // 0 = no age-based deletion
	LogCompress   bool // gzip rotated files
	DataDir       string
	CORSOrigins   []string
	SwaggerHost   string

	// 12.2 Query path
	MinQueryLen       int
	MaxQueryLatencyMs int
	DefaultLimit      int
	MaxLimit          int

	// Max size (MB) accepted for /upload and /restore request bodies.
	MaxUploadMB int

	// 12.3 HotTrieCache
	WordCap            int
	PromotionThreshold int
	PromotionWindowSec int
	PromotionMaxWords  int
	PromotionQueueSize int
	PromotionWorkers   int

	// 12.4 QueryLedger + snapshot (warm restart A)
	LedgerIdleTTLSec          int
	LedgerSnapshotIntervalSec int
	LedgerSnapshotPath        string // empty => <DataDir>/<list>/ledger.snapshot.gob

	// 12.5 Pinned prefixes (warm restart B)
	PinnedPrefixesFile     string // empty => <DataDir>/<list>/pinned_prefixes.txt
	PinnedWarmupTimeoutSec int
	PinnedWarmupEnabled    bool

	// 12.6 Corpus on disk
	CorpusSortChunkMB  int
	SkipIndexStride    int
	CorpusReadBufferKB int
	CorpusWarmOnBoot   bool

	// 12.7 UsageTracker
	UsageTrackerEnabled      bool
	UsagePrefixDepth         int
	UsageSurfacedEnabled     bool
	UsageSnapshotIntervalSec int

	// 12.8 Corpus lifecycle
	CorpusVersionsKept     int
	PruneMaxHits           int
	CorpusStagingDir       string // empty => <DataDir>/<list>/versions/staging
	ApplyMigrateUsage      bool
	RestartFlushTimeoutSec int

	// 12.9 Security for admin endpoints
	AdminToken      string   // ADMIN_TOKEN — empty = admin endpoints return 503
	AdminAllowedIPs []string // ADMIN_ALLOWED_IPS — comma-separated list; empty = no IP restriction

	// 12.10 Swagger UI
	SwaggerEnabled  bool   // SWAGGER_ENABLED — default true; set false to disable /docs
	SwaggerProtect  bool   // SWAGGER_PROTECT — default false; if true, /docs/* requires admin token too
	SwaggerUser     string // SWAGGER_USER — if set with SWAGGER_PASSWORD, /docs requires Basic Auth
	SwaggerPassword string // SWAGGER_PASSWORD — paired with SWAGGER_USER
}

// Load reads .env (if present), resolves every env var, validates, and logs
// the effective non-default values at info level.
func Load() (*Config, error) {
	if err := godotenv.Load(); err != nil {
		slog.Debug("no .env file loaded", "err", err)
	}

	c := &Config{
		// 12.1
		Host:          getEnv("API_HOST", "0.0.0.0"),
		Port:          getInt("API_PORT", 8001),
		LogLevel:      getEnv("LOG_LEVEL", "info"),
		LogFile:       getEnv("LOG_FILE", "logs/go-suggest.log"),
		LogMaxSizeMB:  getInt("LOG_MAX_SIZE_MB", 5),
		LogMaxBackups: getInt("LOG_MAX_BACKUPS", 10),
		LogMaxAgeDays: getInt("LOG_MAX_AGE_DAYS", 0),
		LogCompress:   getBool("LOG_COMPRESS", false),
		DataDir:       getEnv("DATA_DIR", "data"),
		CORSOrigins:   strings.Split(getEnv("CORS_ORIGINS", "*"), ","),
		SwaggerHost:   getEnv("SWAGGER_HOST", ""),

		// 12.2
		MinQueryLen:       getInt("MIN_QUERY_LEN", 2),
		MaxQueryLatencyMs: getInt("MAX_QUERY_LATENCY_MS", 200),
		DefaultLimit:      getInt("DEFAULT_LIMIT", 10),
		MaxLimit:          getInt("MAX_LIMIT", 100),
		MaxUploadMB:       getInt("MAX_UPLOAD_MB", 2048),

		// 12.3
		WordCap:            getInt("WORD_CAP", 100_000),
		PromotionThreshold: getInt("PROMOTION_THRESHOLD", 20),
		PromotionWindowSec: getInt("PROMOTION_WINDOW_SEC", 60),
		PromotionMaxWords:  getInt("PROMOTION_MAX_WORDS", 50_000),
		PromotionQueueSize: getInt("PROMOTION_QUEUE_SIZE", 256),
		PromotionWorkers:   getInt("PROMOTION_WORKERS", 1),

		// 12.4
		LedgerIdleTTLSec:          getInt("LEDGER_IDLE_TTL_SEC", 600),
		LedgerSnapshotIntervalSec: getInt("LEDGER_SNAPSHOT_INTERVAL_SEC", 60),
		LedgerSnapshotPath:        getEnv("LEDGER_SNAPSHOT_PATH", ""),

		// 12.5
		PinnedPrefixesFile:     getEnv("PINNED_PREFIXES_FILE", ""),
		PinnedWarmupTimeoutSec: getInt("PINNED_WARMUP_TIMEOUT_SEC", 30),
		PinnedWarmupEnabled:    getBool("PINNED_WARMUP_ENABLED", true),

		// 12.6
		CorpusSortChunkMB:  getInt("CORPUS_SORT_CHUNK_MB", 256),
		SkipIndexStride:    getInt("SKIP_INDEX_STRIDE", 4096),
		CorpusReadBufferKB: getInt("CORPUS_READ_BUFFER_KB", 64),
		CorpusWarmOnBoot:   getBool("CORPUS_WARM_ON_BOOT", true),

		// 12.7
		UsageTrackerEnabled:      getBool("USAGE_TRACKER_ENABLED", true),
		UsagePrefixDepth:         getInt("USAGE_PREFIX_DEPTH", 3),
		UsageSurfacedEnabled:     getBool("USAGE_SURFACED_ENABLED", true),
		UsageSnapshotIntervalSec: getInt("USAGE_SNAPSHOT_INTERVAL_SEC", 300),

		// 12.8
		CorpusVersionsKept:     getInt("CORPUS_VERSIONS_KEPT", 3),
		PruneMaxHits:           getInt("PRUNE_MAX_HITS", 0),
		CorpusStagingDir:       getEnv("CORPUS_STAGING_DIR", ""),
		ApplyMigrateUsage:      getBool("APPLY_MIGRATE_USAGE", true),
		RestartFlushTimeoutSec: getInt("RESTART_FLUSH_TIMEOUT_SEC", 10),

		// 12.9 Security
		AdminToken:      getEnv("ADMIN_TOKEN", ""),
		AdminAllowedIPs: splitAndTrim(getEnv("ADMIN_ALLOWED_IPS", "")),

		// 12.10 Swagger
		SwaggerEnabled:  getBool("SWAGGER_ENABLED", true),
		SwaggerProtect:  getBool("SWAGGER_PROTECT", false),
		SwaggerUser:     getEnv("SWAGGER_USER", ""),
		SwaggerPassword: getEnv("SWAGGER_PASSWORD", ""),
	}

	if err := c.validate(); err != nil {
		return nil, err
	}
	c.logEffective()
	return c, nil
}

// validate enforces the rules documented in plan.md §12.11.
func (c *Config) validate() error {
	if c.MinQueryLen < 1 {
		slog.Warn("MIN_QUERY_LEN below 1, forcing to 1", "given", c.MinQueryLen)
		c.MinQueryLen = 1
	}
	if c.WordCap <= 0 {
		return fmt.Errorf("WORD_CAP must be positive, got %d", c.WordCap)
	}
	if c.PromotionMaxWords <= 0 {
		return fmt.Errorf("PROMOTION_MAX_WORDS must be positive, got %d", c.PromotionMaxWords)
	}
	if c.WordCap < c.PromotionMaxWords {
		return fmt.Errorf(
			"WORD_CAP (%d) < PROMOTION_MAX_WORDS (%d): a single promoted trie would not fit",
			c.WordCap, c.PromotionMaxWords,
		)
	}
	if c.SkipIndexStride <= 0 {
		return fmt.Errorf("SKIP_INDEX_STRIDE must be positive, got %d", c.SkipIndexStride)
	}
	if c.UsagePrefixDepth < 1 || c.UsagePrefixDepth > 8 {
		return fmt.Errorf("USAGE_PREFIX_DEPTH must be 1..8, got %d", c.UsagePrefixDepth)
	}
	if c.CorpusVersionsKept < 1 {
		return fmt.Errorf("CORPUS_VERSIONS_KEPT must be >= 1, got %d", c.CorpusVersionsKept)
	}
	if c.MaxQueryLatencyMs < 10 {
		return fmt.Errorf("MAX_QUERY_LATENCY_MS must be >= 10, got %d", c.MaxQueryLatencyMs)
	}
	return nil
}

// logEffective prints the effective config at info so operators see the
// actual values in the log stream (useful when debugging env precedence).
func (c *Config) logEffective() {
	slog.Info("config loaded",
		"host", c.Host,
		"port", c.Port,
		"data_dir", c.DataDir,
		"min_query_len", c.MinQueryLen,
		"max_query_latency_ms", c.MaxQueryLatencyMs,
		"word_cap", c.WordCap,
		"promotion_threshold", c.PromotionThreshold,
		"promotion_window_sec", c.PromotionWindowSec,
		"promotion_max_words", c.PromotionMaxWords,
		"ledger_snapshot_interval_sec", c.LedgerSnapshotIntervalSec,
		"pinned_warmup_enabled", c.PinnedWarmupEnabled,
		"skip_index_stride", c.SkipIndexStride,
		"usage_tracker_enabled", c.UsageTrackerEnabled,
		"usage_prefix_depth", c.UsagePrefixDepth,
		"corpus_versions_kept", c.CorpusVersionsKept,
		"admin_auth", adminAuthLabel(c),
	)
}

// adminAuthLabel summarises the admin security posture for the boot log.
// Never includes the actual token — just a status word.
func adminAuthLabel(c *Config) string {
	if c.AdminToken == "" {
		return "disabled (admin endpoints return 503)"
	}
	if len(c.AdminAllowedIPs) > 0 {
		return "token+ip-allowlist"
	}
	return "token"
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getInt(key string, fallback int) int {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		slog.Warn("invalid int env var, using default", "key", key, "raw", raw, "default", fallback)
		return fallback
	}
	return n
}

func getBool(key string, fallback bool) bool {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	b, err := strconv.ParseBool(raw)
	if err != nil {
		slog.Warn("invalid bool env var, using default", "key", key, "raw", raw, "default", fallback)
		return fallback
	}
	return b
}

// splitAndTrim splits a comma-separated string, trims whitespace, and drops
// empty entries. Used for CSV-style env vars like ADMIN_ALLOWED_IPS.
func splitAndTrim(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
