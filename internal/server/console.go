package server

import (
	"embed"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/filesystem"

	"go-suggest-neo/internal/ledger"
	"go-suggest-neo/internal/lifecycle"
)

//go:embed ui/*
var consoleFiles embed.FS

func registerConsole(app *fiber.App) {
	ui, err := fs.Sub(consoleFiles, "ui")
	if err != nil {
		panic(err)
	}
	app.Get("/console", func(c *fiber.Ctx) error { return c.Redirect("/console/") })
	app.Use("/console", filesystem.New(filesystem.Config{Root: http.FS(ui), Browse: false}))
}

func registerProjectAPI(app *fiber.App, projects *ProjectManager, admin fiber.Handler) {
	app.Get("/api/projects", admin, func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"projects": projects.Names(), "node": "local"})
	})
	app.Post("/api/projects", admin, func(c *fiber.Ctx) error {
		var body struct {
			Name string `json:"name"`
		}
		if err := c.BodyParser(&body); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "invalid JSON body")
		}
		name := strings.TrimSpace(body.Name)
		inst, err := projects.Create(name)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
		return c.Status(fiber.StatusCreated).JSON(projectSummary(inst, name))
	})
	app.Delete("/api/projects/:name", admin, func(c *fiber.Ctx) error {
		if err := projects.Delete(c.UserContext(), c.Params("name")); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
		return c.SendStatus(fiber.StatusNoContent)
	})
	app.Get("/api/projects/:name/overview", admin, func(c *fiber.Ctx) error {
		inst, ok := projects.Get(c.Params("name"))
		if !ok {
			return fiber.NewError(fiber.StatusNotFound, "project not found")
		}
		return c.JSON(projectOverview(inst, c.Params("name")))
	})
	app.Post("/api/projects/:name/upload", admin, projectUploadHandler(projects))
	app.Post("/api/projects/:name/reload", admin, func(c *fiber.Ctx) error {
		inst, ok := projects.Get(c.Params("name"))
		if !ok {
			return fiber.NewError(fiber.StatusNotFound, "project not found")
		}
		if err := inst.Reload(c.UserContext()); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "reload failed: "+err.Error())
		}
		return c.JSON(projectSummary(inst, c.Params("name")))
	})
}

func projectUploadHandler(projects *ProjectManager) fiber.Handler {
	return func(c *fiber.Ctx) error {
		inst, ok := projects.Get(c.Params("name"))
		if !ok {
			return fiber.NewError(fiber.StatusNotFound, "project not found")
		}
		column := strings.TrimSpace(c.FormValue("column"))
		if column == "" {
			return fiber.NewError(fiber.StatusBadRequest, "form field 'column' is required")
		}
		mode := strings.ToLower(strings.TrimSpace(c.FormValue("mode")))
		if mode == "" {
			mode = "merge"
		}
		if mode != "merge" && mode != "replace" {
			return fiber.NewError(fiber.StatusBadRequest, "mode must be 'merge' or 'replace'")
		}
		fh, err := c.FormFile("file")
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "form file 'file' (a .csv) is required")
		}
		tmp, err := os.CreateTemp("", "bedrock-project-upload-*.csv")
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		tmpPath := tmp.Name()
		_ = tmp.Close()
		defer os.Remove(tmpPath)
		if err := c.SaveFile(fh, tmpPath); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "save upload: "+err.Error())
		}
		result, err := inst.IngestCSV(c.UserContext(), tmpPath, column, mode)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
		if err := appendSourceFile(inst.ListDir(), SourceFile{
			Name: filepath.Base(fh.Filename), Column: column, Mode: mode,
			ValuesRead: result.ValuesRead, WordCount: result.WordCount,
			Version: result.NewVersion, ImportedAt: time.Now().UTC(),
		}); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "save source history: "+err.Error())
		}
		return c.JSON(fiber.Map{"status": "ok", "mode": result.Mode, "values_read": result.ValuesRead, "new_version": result.NewVersion, "corpus_words": result.WordCount})
	}
}

func projectSummary(inst *Instance, name string) fiber.Map {
	out := fiber.Map{"name": name, "corpus_words": corpusWords(inst), "node": "local"}
	if version, err := lifecycle.ReadCurrent(inst.Paths()); err == nil {
		out["version"] = version
	}
	return out
}

func projectOverview(inst *Instance, name string) fiber.Map {
	out := projectSummary(inst, name)
	eng := inst.Engine()
	if eng == nil {
		return out
	}
	live := gatherLiveStats(inst)
	out["cache"] = fiber.Map{
		"hot_words": live.HotWords, "cold_words": live.ColdWords,
		"hot_entries": live.HotEntries, "hot_word_cap": live.HotWordCap,
		"hit_rate": live.HitRate, "lookups_hit": live.LookupsHit,
		"lookups_miss": live.LookupsMiss, "evicted": live.Evicted,
	}
	out["hot_prefixes"] = eng.Cache().Entries()
	out["cold_prefixes"] = coldPrefixes(eng.Ledger().Snapshot())
	if files, err := readSourceFiles(inst.ListDir()); err == nil {
		out["source_files"] = files
	} else {
		out["source_files_error"] = err.Error()
	}
	return out
}

func coldPrefixes(entries []ledger.SnapshotEntry) []fiber.Map {
	out := make([]fiber.Map, 0, len(entries))
	for _, entry := range entries {
		if !entry.Promoted {
			out = append(out, fiber.Map{"prefix": entry.Prefix, "hits": entry.Hits, "last_seen": entry.LastSeen})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["hits"].(uint32) > out[j]["hits"].(uint32) })
	if len(out) > 100 {
		return out[:100]
	}
	return out
}
