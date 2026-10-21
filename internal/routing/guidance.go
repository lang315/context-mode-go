package routing

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lang315/context-mode-go/internal/hookio"
)

// guidanceOnce ports guidanceOnce (routing.mjs:121-144): it reports true the
// first time guidance of typ is due in session sid. The marker is an empty
// file created with O_EXCL, so concurrent hooks agree on one winner; any
// error (EEXIST, an unwritable dir) means no guidance, as in TS. TS also kept
// a process-wide in-memory set, which a one-shot hook never consults.
func guidanceOnce(runDir, sid, typ string) bool {
	dir := filepath.Join(runDir, "guidance-"+hookio.FileID(sid))
	_ = os.MkdirAll(dir, 0o700) // an error shows up in the open below
	f, err := os.OpenFile(filepath.Join(dir, typ), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

// PruneGuidance removes the guidance-* dirs under runDir whose mtime is more
// than maxAge before now. TS never cleaned them (R1 P18); SessionStart calls
// this on startup (spec §4). Errors are ignored: a dir left behind only keeps
// guidance off for a session that is long over.
func PruneGuidance(runDir string, now time.Time, maxAge time.Duration) {
	ents, err := os.ReadDir(runDir)
	if err != nil {
		return
	}
	for _, e := range ents {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "guidance-") {
			continue
		}
		if fi, err := e.Info(); err == nil && now.Sub(fi.ModTime()) > maxAge {
			_ = os.RemoveAll(filepath.Join(runDir, e.Name()))
		}
	}
}
