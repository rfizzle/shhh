package skill

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rfizzle/shhh/internal/provider"
)

// ToolName is the activation tool. It reads a file the model could read
// itself, so it is a read-only tool and runs without approval — what it
// adds over read_file is that the name is constrained to the catalog, the
// frontmatter is stripped, and the bundled files are listed alongside.
const ToolName = "skill"

// ToolDefinition is the activation tool for this catalog. The name
// parameter is an enum of what actually loaded, so a skill the model
// misremembers is a schema error rather than a round spent on an apology.
func ToolDefinition(c *Catalog) provider.Tool {
	names, _ := json.Marshal(c.Names())
	return provider.Tool{
		Name:        ToolName,
		Description: "Load a skill's full instructions by name. Call it when the current task matches one of the available skills, before starting the work; the result is the skill's instructions plus the directory its supporting files live in.",
		Parameters: json.RawMessage(fmt.Sprintf(`{
			"type": "object",
			"properties": {
				"name": {"type": "string", "enum": %s, "description": "The skill to load"}
			},
			"required": ["name"]
		}`, names)),
	}
}

// Execute activates one skill by name.
func (c *Catalog) Execute(raw json.RawMessage) (string, error) {
	var args struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	s, ok := c.Find(strings.TrimSpace(args.Name))
	if !ok {
		return "", fmt.Errorf("no skill named %q (available: %s)", args.Name, strings.Join(c.Names(), ", "))
	}
	return Content(s)
}

// WrapExecutor routes the skill tool to this catalog and everything else
// to next.
func (c *Catalog) WrapExecutor(next func(name string, args json.RawMessage) (string, error)) func(string, json.RawMessage) (string, error) {
	return func(name string, args json.RawMessage) (string, error) {
		if name == ToolName {
			return c.Execute(args)
		}
		return next(name, args)
	}
}

// Bounds on the resource listing: a skill that bundles a vendored tree
// should not put every file of it in the conversation.
const (
	maxResources     = 50
	maxResourceDepth = 4
)

// Resources lists the files bundled beside a SKILL.md, relative to the
// skill directory, sorted, at most maxResources deep to maxResourceDepth;
// partial reports that the cap was hit. Version-control and dependency
// directories are skipped the way discovery skips them.
func Resources(dir string) (files []string, partial bool) {
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, rerr := filepath.Rel(dir, path)
		if rerr != nil || rel == "." {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "__pycache__", ".venv":
				return fs.SkipDir
			}
			if strings.Count(rel, string(os.PathSeparator)) >= maxResourceDepth {
				return fs.SkipDir
			}
			return nil
		}
		if rel == "SKILL.md" {
			return nil
		}
		if len(files) >= maxResources {
			partial = true
			return fs.SkipAll
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(files)
	return files, partial
}
