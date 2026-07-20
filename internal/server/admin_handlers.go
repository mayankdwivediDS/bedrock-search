package server

import (
	"bufio"
	"log/slog"
	"os"
	"strings"

	"github.com/gofiber/fiber/v2"
)

// uploadHandler — POST /upload (multipart/form-data).
//
// Fields:
//
//	file    the .csv file            (required)
//	column  header name to ingest    (required)
//	mode    "merge" (default) | "replace"
//
// merge   = add the column's values to the existing corpus, deduplicated.
// replace = the CSV becomes the entire corpus.
//
// The corpus reloads in-process, so the new words are queryable as soon as
// the response returns.
func uploadHandler(inst *Instance) fiber.Handler {
	return func(c *fiber.Ctx) error {
		column := strings.TrimSpace(c.FormValue("column"))
		if column == "" {
			return fiber.NewError(fiber.StatusBadRequest,
				"form field 'column' is required (the CSV column header to ingest)")
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

		tmp, err := os.CreateTemp("", "neo-upload-*.csv")
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		tmpPath := tmp.Name()
		_ = tmp.Close()
		defer os.Remove(tmpPath)

		if err := c.SaveFile(fh, tmpPath); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "save upload: "+err.Error())
		}

		res, err := inst.IngestCSV(c.UserContext(), tmpPath, column, mode)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
		return c.JSON(fiber.Map{
			"status":       "ok",
			"mode":         res.Mode,
			"values_read":  res.ValuesRead,
			"new_version":  res.NewVersion,
			"corpus_words": res.WordCount,
		})
	}
}

// reloadHandler — POST /reload. Rebuilds the engine from current.version on
// disk. Useful if you edited the data directory by hand.
func reloadHandler(inst *Instance) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if err := inst.Reload(c.UserContext()); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "reload failed: "+err.Error())
		}
		return c.JSON(fiber.Map{
			"status":       "reloaded",
			"corpus_words": corpusWords(inst),
		})
	}
}

// backupHandler — GET /backup. Streams the whole data directory as one zip.
func backupHandler(inst *Instance) fiber.Handler {
	return func(c *fiber.Ctx) error {
		// Flush in-memory state so the backup captures the latest counters.
		if e := inst.Engine(); e != nil {
			_ = e.FlushUsageSnapshot()
			_ = e.FlushLedgerSnapshot()
		}
		dataDir := inst.cfg.DataDir

		c.Set(fiber.HeaderContentType, "application/zip")
		c.Set(fiber.HeaderContentDisposition, `attachment; filename="go-suggest-backup.zip"`)
		c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
			if err := writeBackupZip(w, dataDir); err != nil {
				slog.Error("backup stream failed", "err", err)
			}
			_ = w.Flush()
		})
		return nil
	}
}

// restoreHandler — POST /restore (multipart/form-data, field 'file' = .zip).
// Replaces the data directory with the backup and brings it live.
func restoreHandler(inst *Instance) fiber.Handler {
	return func(c *fiber.Ctx) error {
		fh, err := c.FormFile("file")
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest,
				"form file 'file' (a backup .zip from /backup) is required")
		}
		tmp, err := os.CreateTemp("", "neo-restore-*.zip")
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		tmpPath := tmp.Name()
		_ = tmp.Close()
		defer os.Remove(tmpPath)

		if err := c.SaveFile(fh, tmpPath); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "save upload: "+err.Error())
		}

		if err := inst.RestoreFromZip(c.UserContext(), tmpPath); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
		return c.JSON(fiber.Map{
			"status":       "restored",
			"corpus_words": corpusWords(inst),
		})
	}
}

// blacklistRequest is the POST /blacklist JSON body.
type blacklistRequest struct {
	Words []string `json:"words"`
}

// blacklistHandler — POST /blacklist?reload=true|false.
//
// Adds words to the persistent blacklist so they are never ingested again.
// With reload=true (the default), it also rebuilds the live corpus without
// any blacklisted word and swaps it in. With reload=false it only records
// the words (applied on the next /upload or /blacklist?reload=true).
func blacklistHandler(inst *Instance) fiber.Handler {
	return func(c *fiber.Ctx) error {
		var body blacklistRequest
		if err := c.BodyParser(&body); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "invalid JSON body; expected {\"words\": [...]}")
		}
		if len(body.Words) == 0 {
			return fiber.NewError(fiber.StatusBadRequest, "'words' must be a non-empty array")
		}
		reload := c.QueryBool("reload", true)

		res, err := inst.ApplyBlacklist(c.UserContext(), body.Words, reload)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		out := fiber.Map{
			"status":          "ok",
			"added":           res.Added,
			"blacklist_total": res.BlacklistTotal,
			"reloaded":        res.Reloaded,
		}
		if res.Reloaded {
			out["removed_from_corpus"] = res.Removed
			out["new_version"] = res.NewVersion
			out["corpus_words"] = res.WordCount
		}
		return c.JSON(out)
	}
}

// listBlacklistHandler — GET /blacklist. Returns the current blacklist.
func listBlacklistHandler(inst *Instance) fiber.Handler {
	return func(c *fiber.Ctx) error {
		words, err := sortedBlacklist(inst.listDir)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		return c.JSON(fiber.Map{"count": len(words), "words": words})
	}
}

// corpusWords returns the live corpus word count, or 0 if no engine is
// currently mounted (e.g. mid-restore).
func corpusWords(inst *Instance) int64 {
	if e := inst.Engine(); e != nil {
		return e.Reader().WordCount()
	}
	return 0
}
