package tsimport

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lang315/context-mode-go/internal/config"
	"github.com/lang315/context-mode-go/internal/store"
)

// markKey is the meta key that records a finished import (spec §3.2).
const markKey = "ts_import"

// sqliteTime is the layout of TS datetime('now') values.
const sqliteTime = "2006-01-02 15:04:05"

// ErrAlreadyImported is returned when cmgo.db records an import and Force
// is not set.
var ErrAlreadyImported = errors.New("TS data already imported")

// Options configures Run.
type Options struct {
	Root            string // TS data root, holding sessions/ and content/
	DataDir         string // $CMGO_DIR: the copies go to DataDir/run/import-*
	DBPath          string // cmgo.db
	DryRun, Force   bool
	Now             time.Time
	SourceRetention time.Duration // sources older than this are not imported
}

// Report counts what Run imported and skipped. In a dry run it counts what
// a real run would do now.
type Report struct {
	Root                                          string
	Sessions, InactiveSessions, NoProjectSessions int
	Events, DupEvents                             int
	Sources, Chunks                               int
	ExpiredSources, NoProjectSources              int
	ExistingSources                               int // a cmgo source with the same (project, label) won
}

// DefaultRoot returns the first TS data root that exists, probing
// $CONTEXT_MODE_DIR, <claudeDir>/context-mode and ~/.claude/context-mode
// (R4 §D.1), or "" when none does.
func DefaultRoot(getenv func(string) string, home, claudeDir string) string {
	for _, root := range []string{
		getenv("CONTEXT_MODE_DIR"),
		filepath.Join(claudeDir, "context-mode"),
		filepath.Join(home, ".claude", "context-mode"),
	} {
		if root != "" && hasData(root) {
			return root
		}
	}
	return ""
}

// hasData reports whether root has a sessions/ or content/ directory.
func hasData(root string) bool {
	for _, dir := range []string{"sessions", "content"} {
		if fi, err := os.Stat(filepath.Join(root, dir)); err == nil && fi.IsDir() {
			return true
		}
	}
	return false
}

// Run imports the TS data under o.Root into o.DBPath. Every TS file is
// copied into a temp dir first and only the copy is opened; the temp dir is
// removed on return. A dry run imports into a copy of cmgo.db, so its counts
// are exact and cmgo.db is not touched.
func Run(o Options) (Report, error) {
	r := Report{Root: o.Root}
	if !hasData(o.Root) {
		return r, fmt.Errorf("no sessions/ or content/ directory in %s", o.Root)
	}
	run := filepath.Join(o.DataDir, "run")
	if err := os.MkdirAll(run, 0o755); err != nil {
		return r, err
	}
	tmp, err := os.MkdirTemp(run, "import-")
	if err != nil {
		return r, err
	}
	defer os.RemoveAll(tmp)

	st, err := openTarget(o, tmp)
	if err != nil {
		return r, err
	}
	defer st.Close()
	if mark, ok, err := st.Meta(markKey); err != nil {
		return r, err
	} else if ok && !o.Force {
		return r, fmt.Errorf("%w (%s); run with --force to import again", ErrAlreadyImported, mark)
	}

	sessions, projects, err := readSessionDBs(filepath.Join(o.Root, "sessions"), filepath.Join(tmp, "sessions"))
	if err != nil {
		return r, err
	}
	cutoff := o.Now.Add(-store.SessionRetention).UTC().Format(sqliteTime)
	for _, s := range sessions {
		switch {
		case s.lastEventAt < cutoff:
			r.InactiveSessions++
			continue
		case s.projectDir == "":
			r.NoProjectSessions++
			continue
		}
		ins, dupes, err := st.ImportEvents(store.ImportedSession{ID: s.id, Project: s.projectDir,
			StartedAt: s.startedAt, LastEventAt: s.lastEventAt, CompactCount: s.compact}, s.events)
		if err != nil {
			return r, fmt.Errorf("session %s: %w", s.id, err)
		}
		r.Sessions++
		r.Events += ins
		r.DupEvents += dupes
	}

	if err := importContent(st, o, filepath.Join(tmp, "content"), projects, &r); err != nil {
		return r, err
	}
	if o.DryRun {
		return r, nil
	}
	return r, st.SetMeta(markKey, fmt.Sprintf("%s %d %d", o.Now.UTC().Format(time.RFC3339), r.Sessions, r.Sources))
}

// openTarget opens cmgo.db, or for a dry run a copy of it (a fresh DB when
// cmgo.db does not exist yet) inside tmp.
func openTarget(o Options, tmp string) (*store.Store, error) {
	if !o.DryRun {
		return store.Open(o.DBPath)
	}
	dir := filepath.Join(tmp, "cmgo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, filepath.Base(o.DBPath))
	if _, err := os.Stat(o.DBPath); err == nil {
		if path, err = copyDB(o.DBPath, dir); err != nil {
			return nil, err
		}
	}
	return store.Open(path)
}

// readSessionDBs reads every <dir>/*.db through a copy in tmp. Sessions
// found in several DBs (worktrees) are merged into one, keeping DB name
// order and id order within a DB; their projects are normalized. projects
// maps each file's hash prefix (the name before "__" or ".db") to the most
// common project of all its sessions, active or not, for the content DBs;
// the unsuffixed <h16>.db sorts first and wins.
func readSessionDBs(dir, tmp string) ([]tsSession, map[string]string, error) {
	files, err := copies(dir, tmp)
	if err != nil {
		return nil, nil, err
	}
	var out []tsSession
	index := map[string]int{}
	projects := map[string]string{}
	for _, f := range files {
		c, err := openCopy(f.copy)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", f.src, err)
		}
		sessions, err := c.sessions()
		c.Close()
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", f.src, err)
		}
		var fileProjects []string
		for _, s := range sessions {
			if s.projectDir != "" {
				s.projectDir = config.NormalizeProject(s.projectDir)
				fileProjects = append(fileProjects, s.projectDir)
			}
			i, ok := index[s.id]
			if !ok {
				index[s.id] = len(out)
				out = append(out, s)
				continue
			}
			m := &out[i]
			m.events = append(m.events, s.events...)
			m.startedAt = minTime(m.startedAt, s.startedAt)
			m.lastEventAt = max(m.lastEventAt, s.lastEventAt)
			m.compact = max(m.compact, s.compact)
			if m.projectDir == "" {
				m.projectDir = s.projectDir
			}
		}
		prefix, _, _ := strings.Cut(strings.TrimSuffix(filepath.Base(f.src), ".db"), "__")
		if p := mostCommon(fileProjects); p != "" && projects[prefix] == "" {
			projects[prefix] = p
		}
	}
	return out, projects, nil
}

// importContent imports the fresh sources of every content DB whose
// <h16>.db name has a project in projects.
func importContent(st *store.Store, o Options, tmp string, projects map[string]string, r *Report) error {
	files, err := copies(filepath.Join(o.Root, "content"), tmp)
	if err != nil {
		return err
	}
	cutoff := o.Now.Add(-o.SourceRetention).UTC().Format(sqliteTime)
	for _, f := range files {
		c, err := openCopy(f.copy)
		if err != nil {
			return fmt.Errorf("%s: %w", f.src, err)
		}
		sources, err := c.sources()
		c.Close()
		if err != nil {
			return fmt.Errorf("%s: %w", f.src, err)
		}
		project := projects[strings.TrimSuffix(filepath.Base(f.src), ".db")]
		for _, s := range sources {
			switch {
			case s.indexedAt < cutoff:
				r.ExpiredSources++
				continue
			case project == "":
				r.NoProjectSources++
				continue
			}
			ok, err := st.ImportSource(project, s.label, s.indexedAt, s.chunks)
			if err != nil {
				return fmt.Errorf("%s: source %q: %w", f.src, s.label, err)
			}
			if !ok {
				r.ExistingSources++
				continue
			}
			r.Sources++
			r.Chunks += len(s.chunks)
		}
	}
	return nil
}

// copied is a TS DB and its copy.
type copied struct{ src, copy string }

// copies copies every <dir>/*.db (with -wal/-shm) into tmp, in name order.
// A missing dir has no DBs.
func copies(dir, tmp string) ([]copied, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.db"))
	if err != nil || len(files) == 0 {
		return nil, err
	}
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return nil, err
	}
	out := make([]copied, 0, len(files))
	for _, f := range files { // Glob sorts by name
		c, err := copyDB(f, tmp)
		if err != nil {
			return nil, err
		}
		out = append(out, copied{src: f, copy: c})
	}
	return out, nil
}
