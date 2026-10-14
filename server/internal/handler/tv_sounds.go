package handler

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"

	"github.com/go-chi/chi/v5"
)

// The TV alarm sounds are third-party recordings (GPL-2.0, see
// docs/tv-sounds-attribution.md) kept out of this repository: the server
// serves them from MULTICA_TV_SOUNDS_DIR to kiosk screens only.

var tvSoundName = regexp.MustCompile(`^[a-z0-9_]{1,64}\.mp3$`)

// GetTVSound serves GET /api/tv/sounds/{file}.
func (h *Handler) GetTVSound(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireKiosk(w, r); !ok {
		return
	}
	name := chi.URLParam(r, "file")
	dir := h.cfg.TV.SoundsDir
	if dir == "" || !tvSoundName.MatchString(name) {
		writeError(w, http.StatusNotFound, "sound not found")
		return
	}
	path := filepath.Join(dir, name)
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		writeError(w, http.StatusNotFound, "sound not found")
		return
	}
	w.Header().Set("Content-Type", "audio/mpeg")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeFile(w, r, path)
}
