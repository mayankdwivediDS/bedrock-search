package server

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"go-suggest-neo/internal/config"
)

func testProjectManager(t *testing.T) *ProjectManager {
	t.Helper()
	cfg := &config.Config{
		DataDir:                   t.TempDir(),
		WordCap:                   100,
		PromotionThreshold:        2,
		PromotionWindowSec:        60,
		PromotionMaxWords:         100,
		PromotionQueueSize:        8,
		PromotionWorkers:          1,
		LedgerIdleTTLSec:          60,
		LedgerSnapshotIntervalSec: 60,
		PinnedWarmupTimeoutSec:    1,
		UsageSnapshotIntervalSec:  60,
		UsagePrefixDepth:          3,
		CorpusSortChunkMB:         1,
		SkipIndexStride:           1,
		CorpusReadBufferKB:        1,
		CorpusVersionsKept:        2,
		DefaultLimit:              10,
		MaxLimit:                  100,
		MinQueryLen:               2,
		MaxQueryLatencyMs:         200,
	}
	m, err := NewProjectManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, name := range m.Names() {
			if inst, ok := m.Get(name); ok {
				inst.Stop(context.Background())
			}
		}
	})
	return m
}

func TestProjectConsoleCreatesIsolatedProject(t *testing.T) {
	mgr := testProjectManager(t)
	app := NewWithProjectManager(mgr)

	req := httptest.NewRequest("POST", "/api/projects", strings.NewReader(`{"name":"catalog-2"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 201 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}
	if _, ok := mgr.Get("catalog-2"); !ok {
		t.Fatal("created project was not registered")
	}

	consoleReq := httptest.NewRequest("GET", "/console/index.html", nil)
	consoleResp, err := app.Test(consoleReq)
	if err != nil {
		t.Fatal(err)
	}
	defer consoleResp.Body.Close()
	if consoleResp.StatusCode != 200 {
		t.Fatalf("console status=%d", consoleResp.StatusCode)
	}
}

func TestValidProjectName(t *testing.T) {
	for _, name := range []string{"catalog", "catalog-2", "a1"} {
		if !validProjectName(name) {
			t.Errorf("%q should be valid", name)
		}
	}
	for _, name := range []string{"", "UPPER", "has space", "-start", "end-", "../../escape"} {
		if validProjectName(name) {
			t.Errorf("%q should be invalid", name)
		}
	}
}
