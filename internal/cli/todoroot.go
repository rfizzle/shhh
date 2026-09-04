package cli

// Where a backlog goes when the working directory is part of no project.
//
// A backlog belongs to a project, and a session standing in one reads that
// project's list whatever the settings say. But a conversation is often
// opened nowhere in particular — a home directory, a downloads folder — and
// a reading list is not kept in a checkout. So there are two answers below
// the project: the directory `todo.root` names, and one global backlog
// beside the person's own settings, which is where a list that belongs to
// nobody's checkout ends up.
// See docs/capabilities/todo.md#where-the-backlog-lives.

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/todo"
)

// globalBacklogDirName is the global backlog's directory, beside the
// person's profiles under the same `todo` directory: everything they wrote
// for shhh's backlog sits in one place. It is a name no profile may take,
// because the two would be the same directory.
const globalBacklogDirName = "backlog"

// backlogElsewhere is the two roots below the project, as these settings
// leave them.
func backlogElsewhere(cfg config.Config) todo.Elsewhere {
	e := todo.Elsewhere{Global: globalBacklogDir()}
	if root := cfg.Todo.Root; root != "" {
		e.Setting = backlogRoot(root)
	}
	return e
}

// backlogRoot resolves what the setting says. A leading `~` is the person's
// home directory, because this key names a place on their machine and that
// is how anyone writes one; anything else relative resolves against the
// directory the settings file lives in, the way a wording path does, since a
// relative path meaning "wherever this session was opened" would name a
// different backlog in every terminal.
func backlogRoot(path string) string {
	if path == "~" || strings.HasPrefix(path, "~"+string(filepath.Separator)) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~"))
		}
	}
	return promptPath(path)
}

// globalBacklogDir is where that list lives.
func globalBacklogDir() string {
	return filepath.Join(filepath.Dir(config.WritePath()), todoProfileDirName, globalBacklogDirName)
}
