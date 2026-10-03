package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/reports"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/ui/components"
)

func probeStore(context.Context, config.Config) doctorFinding {
	db, err := openStore()
	if err != nil {
		return doctorStore("", 0, err)
	}
	// Opening runs the migrations, so a store that opens is a store this
	// build can read as well as find. Nothing is queried beyond that: the
	// question here is whether the file works, and the surfaces that read it
	// count their own rows.
	defer db.Close()
	path := doctorStorePath()
	var size int64
	if info, statErr := os.Stat(path); statErr == nil {
		size = info.Size()
	}
	return doctorStore(path, size, nil)
}

// doctorStorePath is where the local store lives, for the row to name. It asks
// storage rather than deriving it again: this file had its own copy of the
// rule, and a report that names a different path from the one the store
// actually opens is worse than no path at all.
func doctorStorePath() string {
	dir, err := storage.Dir()
	if err != nil {
		return "shhh.db"
	}
	return filepath.Join(dir, "shhh.db")
}

// doctorStore reads the local store: history, snippets, metrics and chat logs
// all live in it, so a store that will not open is the check that explains
// four other things being empty.
func doctorStore(path string, size int64, err error) doctorFinding {
	if err != nil {
		return doctorFinding{
			Subject: "the local store did not open", Detail: err.Error(), Outcome: "unreadable",
			State:       components.DoctorFailed,
			Consequence: "history, snippets and metrics will all be empty, and nothing new will be recorded",
			FixLabel:    "show the two things to check",
			Fix: []string{
				"ls -l " + shortPath(doctorStorePath()),
				"the directory must be writable; delete the file to start a fresh store",
			},
		}
	}
	return doctorFinding{
		Subject: shortPath(path),
		Detail:  "opened · migrations current · " + doctorBytes(size),
		Outcome: "ok",
	}
}

// doctorBytes is a file size in the units a reader thinks in. A store nobody
// has written to yet is stated as such rather than as `0 B`, which reads like
// a fault where the truth is a fresh install.
func doctorBytes(n int64) string {
	switch {
	case n <= 0:
		return "nothing recorded yet"
	case n < 1<<10:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.0f kB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
}

func probeLogs(context.Context, config.Config) doctorFinding {
	path, err := logPath()
	if err != nil {
		return doctorLogs("", 0, err)
	}
	// A log that is not there is the ordinary case on a machine where
	// nothing has gone wrong, so its absence is a size of zero rather than
	// an error: the row's job is to name the file a failure would be written
	// to, and it can do that either way.
	var size int64
	if info, statErr := os.Stat(path); statErr == nil {
		size = info.Size()
	}
	return doctorLogs(path, size, nil)
}

// doctorLogs reads the diagnostic log: where it is, and how much is in it.
// This is the row `shhh logs` is the reader for, so the path it names is the
// path that command opens — both ask logPath.
//
// It does not ask whether the file can be written, and the row above it is
// why: the store lives in the same directory and opening it is the check
// that fails, loudly, when that directory cannot be used. Naming the same
// fault twice on one screen reads as two faults, and the second one here
// would have to write a file to find out.
func doctorLogs(path string, size int64, err error) doctorFinding {
	if err != nil {
		// There is nowhere for state to go at all: no XDG_DATA_HOME and no
		// home directory to fall back on.
		return doctorFinding{
			Subject: "there is nowhere to write the log", Detail: err.Error(), Outcome: "nowhere",
			State:       components.DoctorWarned,
			Consequence: "a refused request will be reported on the screen and nowhere else",
			FixLabel:    "show where it would go",
			Fix: []string{
				"shhh writes its state under $XDG_DATA_HOME, or ~/.local/share when that is unset",
				"one of the two has to name a directory this user can create",
			},
		}
	}
	return doctorFinding{
		Subject: shortPath(path),
		Detail:  doctorBytes(size),
		Outcome: "ok",
	}
}

func probeReports(context.Context, config.Config) doctorFinding {
	dir, err := reportsDir()
	if err != nil {
		// The store row above already failed loudly on the same missing
		// state directory; this row states its own consequence and stops.
		return doctorReports("", 0, 0, err)
	}
	count, size, err := reports.Census(dir)
	return doctorReports(dir, count, size, err)
}

// doctorReports reads the report store: where it is, how many pages it
// holds, and what they cost in disk. This is the row `shhh reports` is the
// reader for, so the path it names is the one the listing opens — both ask
// reportsDir, and retention is the answer to a store that grows
// (docs/capabilities/reports.md#findable-and-prunable).
func doctorReports(path string, count int, size int64, err error) doctorFinding {
	if err != nil {
		return doctorFinding{
			Subject: "the report store is not readable", Detail: err.Error(), Outcome: "unreadable",
			State:       components.DoctorWarned,
			Consequence: "the report tool is not offered, and pages already made cannot be reopened",
			FixLabel:    "show the place to check",
			Fix: []string{
				"ls -l " + shortPath(path),
				"the directory must be readable; deleting it starts an empty store",
			},
		}
	}
	detail := doctorBytes(size)
	if count > 0 {
		detail = countOf(count, "report", "reports") + " · " + doctorBytes(size)
	}
	return doctorFinding{
		Subject: shortPath(path),
		Detail:  detail,
		Outcome: "ok",
	}
}

func probeOtel(_ context.Context, cfg config.Config) doctorFinding {
	return doctorOtel(cfg.Otel.Endpoint)
}

// doctorOtel reads the one setting that sends the session record off this
// machine, and it reads it rather than reaching it. A collector is somebody
// else's process on somebody else's network: opening a connection to it
// would make this the one check in the run that costs a third party a round
// trip, and a collector that happens to be down is not a fault of this
// machine's — the exporter's own answer to that is to give up quietly and
// write one line to the log.
//
// The row exists so that "my sessions are leaving this machine" is a fact a
// reader can see without opening the config file, which is the same reason
// the store and the log rows name their paths.
func doctorOtel(endpoint string) doctorFinding {
	if strings.TrimSpace(endpoint) == "" {
		return doctorFinding{
			Subject: "the record stays on this machine",
			Outcome: "off",
			State:   components.DoctorSkipped,
		}
	}
	target, err := observe.ParseEndpoint(endpoint)
	if err != nil {
		return doctorFinding{
			Subject: "the collector endpoint is not a URL", Detail: err.Error(), Outcome: "unusable",
			State:       components.DoctorWarned,
			Consequence: "sessions are recorded locally and nothing is exported",
			FixLabel:    "show the shape it takes",
			Fix: []string{
				"shhh config set --global otel.endpoint http://localhost:4318",
				"the scheme decides whether the record crosses the network in the clear, so it is never guessed",
			},
		}
	}
	return doctorFinding{
		Subject: target,
		Detail:  "content-free",
		Outcome: "ok",
	}
}
