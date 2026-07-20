package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode"

	"go-suggest-neo/internal/config"
)

// ProjectManager owns isolated search instances. Each project uses a separate
// <DATA_DIR>/<project> directory and therefore has its own corpus versions,
// cache, usage state, blacklist, and source-file history.
type ProjectManager struct {
	cfg      *config.Config
	mu       sync.RWMutex
	projects map[string]*Instance
}

func NewProjectManager(cfg *config.Config) (*ProjectManager, error) {
	m := &ProjectManager{cfg: cfg, projects: make(map[string]*Instance)}
	if _, err := m.Create("default"); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(cfg.DataDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || name == "default" || !validProjectName(name) {
			continue
		}
		if _, err := m.Create(name); err != nil {
			return nil, fmt.Errorf("load project %q: %w", name, err)
		}
	}
	return m, nil
}

// Default returns the backwards-compatible default instance.
func (m *ProjectManager) Default() *Instance {
	inst, _ := m.Get("default")
	return inst
}

func validProjectName(name string) bool {
	if len(name) < 1 || len(name) > 48 {
		return false
	}
	for _, r := range name {
		if !(unicode.IsLower(r) || unicode.IsDigit(r) || r == '-') {
			return false
		}
	}
	return !strings.HasPrefix(name, "-") && !strings.HasSuffix(name, "-")
}

func (m *ProjectManager) Create(name string) (*Instance, error) {
	if !validProjectName(name) {
		return nil, fmt.Errorf("project name must use lowercase letters, digits, and hyphens (1-48 characters)")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.projects[name]; ok {
		return nil, fmt.Errorf("project %q already exists", name)
	}
	inst, err := NewInstance(m.cfg, name)
	if err != nil {
		return nil, err
	}
	m.projects[name] = inst
	return inst, nil
}

func (m *ProjectManager) Get(name string) (*Instance, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	inst, ok := m.projects[name]
	return inst, ok
}

func (m *ProjectManager) Names() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, 0, len(m.projects))
	for name := range m.projects {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Delete removes a non-default project and its isolated on-disk state.
// It is deliberately unavailable for the default project.
func (m *ProjectManager) Delete(ctx context.Context, name string) error {
	if name == "default" {
		return fmt.Errorf("the default project cannot be deleted")
	}
	m.mu.Lock()
	inst, ok := m.projects[name]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("project %q not found", name)
	}
	delete(m.projects, name)
	m.mu.Unlock()
	inst.Stop(ctx)
	if err := os.RemoveAll(filepath.Join(m.cfg.DataDir, name)); err != nil {
		return fmt.Errorf("remove project data: %w", err)
	}
	return nil
}
