package lansync

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/pocketbase/pocketbase/core"

	"github.com/xemu-cartographer/xemu-cartographer/internal/lansync"
)

// Station-scoped download endpoints (SPEC §4.4) — headless stations pull the
// server-extracted artifacts, under authorizeLAN:
//
//	GET /api/lan/sync/dl/game/{id}  → tar of the extracted disc tree (isos/{id})
//	GET /api/lan/sync/dl/app/{id}   → the stored app zip (apps/{id})
//
// (Profiles reuse GET /api/lan/saves/file/{kind}/{id}.) The manifest hands the
// client the exact `path`, so it builds no URLs.
func init() {
	register(func() {
		Group.GET("/dl/game/{id}", handleGameDownload)
		Group.GET("/dl/app/{id}", handleAppDownload)
	})
}

// handleGameDownload streams the extracted disc tree for an ISO as a tar built
// on the fly from the cached tree (extracted_path). A disc the manifest would
// hide from the caller (gameServable: drifted, or not cleared for a station
// under PD-9) is a 404 here too, so guessing an id gains a station nothing.
func handleGameDownload(e *core.RequestEvent) error {
	id := strings.TrimSpace(e.Request.PathValue("id"))
	if id == "" {
		return e.JSON(http.StatusBadRequest, map[string]string{"error": "id is required"})
	}
	rec, err := e.App.FindRecordById("isos", id)
	if err != nil || rec == nil || !gameServable(rec, isStation(e)) {
		return e.JSON(http.StatusNotFound, map[string]string{"error": "game record not found"})
	}
	if !rec.GetBool("extracted_ready") {
		return e.JSON(http.StatusConflict, map[string]string{
			"error": "extracted tree not ready — the extraction hook has not produced this game yet",
		})
	}
	treeDir := rec.GetString("extracted_path")
	if treeDir == "" {
		return e.JSON(http.StatusConflict, map[string]string{"error": "extracted_path is empty"})
	}

	data, err := lansync.TarDir(treeDir)
	if err != nil {
		return e.JSON(http.StatusInternalServerError, map[string]string{"error": "tar extracted tree: " + err.Error()})
	}

	h := e.Response.Header()
	h.Set("Content-Disposition", "attachment; filename=\""+destName(rec)+".tar\"")
	h.Set("X-Fatx-Footprint-Bytes", strconv.FormatInt(int64(rec.GetInt("footprint_bytes")), 10))
	return e.Blob(http.StatusOK, "application/x-tar", data)
}

// handleAppDownload streams the stored app zip (SPEC §4.4: apps download as zip).
func handleAppDownload(e *core.RequestEvent) error {
	id := strings.TrimSpace(e.Request.PathValue("id"))
	if id == "" {
		return e.JSON(http.StatusBadRequest, map[string]string{"error": "id is required"})
	}
	rec, err := e.App.FindRecordById("apps", id)
	if err != nil || rec == nil {
		return e.JSON(http.StatusNotFound, map[string]string{"error": "app record not found"})
	}
	filename := rec.GetString("file")
	if filename == "" {
		return e.JSON(http.StatusNotFound, map[string]string{"error": "no app zip uploaded for this record"})
	}

	fsys, err := e.App.NewFilesystem()
	if err != nil {
		return e.JSON(http.StatusInternalServerError, map[string]string{"error": "filesystem unavailable"})
	}
	defer fsys.Close()

	// Stream the stored zip (Range-capable via PB's filesystem.Serve).
	e.Response.Header().Set("X-Fatx-Footprint-Bytes", strconv.FormatInt(int64(rec.GetInt("footprint_bytes")), 10))
	key := rec.BaseFilesPath() + "/" + filename
	if err := fsys.Serve(e.Response, e.Request, key, filename); err != nil {
		return e.JSON(http.StatusInternalServerError, map[string]string{"error": "serve failed: " + err.Error()})
	}
	return nil
}
