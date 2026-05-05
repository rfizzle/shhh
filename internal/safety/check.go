package safety

import (
	"regexp"
	"strings"
)

type Warning struct {
	Pattern string
	Risk    string
}

var patterns = []struct {
	re   *regexp.Regexp
	risk string
}{
	{regexp.MustCompile(`^\s*(sudo\s+)?rm\s+(-[^\s]*)?r[^\s]*f`), "recursive forced deletion — may destroy files irrecoverably"},
	{regexp.MustCompile(`^\s*(sudo\s+)?rm\s+(-[^\s]*)?f[^\s]*r`), "recursive forced deletion — may destroy files irrecoverably"},
	{regexp.MustCompile(`^\s*(sudo\s+)?rm\s+-rf\s+/\s*$`), "deletes entire filesystem"},
	{regexp.MustCompile(`^\s*(sudo\s+)?rm\s+-rf\s+~`), "deletes entire home directory"},
	{regexp.MustCompile(`\bdd\s+.*\bof=/dev/`), "writes directly to a device — may destroy disk contents"},
	{regexp.MustCompile(`\bmkfs\.`), "formats a filesystem — all data on target will be lost"},
	{regexp.MustCompile(`\bchmod\s+(-[^\s]*)?\s*-?R\s+777\b`), "recursively removes all permission restrictions"},
	{regexp.MustCompile(`>\s*/dev/sd[a-z]`), "overwrites a raw block device"},
	{regexp.MustCompile(`>\s*/dev/nvme`), "overwrites a raw block device"},
	{regexp.MustCompile(`\b:(){ :\|:& };:`), "fork bomb — will crash the system"},
	{regexp.MustCompile(`\bgit\s+push\s+.*--force\b`), "force push — may overwrite remote history"},
	{regexp.MustCompile(`\bgit\s+push\s+-f\b`), "force push — may overwrite remote history"},
	{regexp.MustCompile(`\bgit\s+reset\s+--hard\b`), "discards all uncommitted changes permanently"},
	{regexp.MustCompile(`(?i)\bdrop\s+(database|table)\b`), "drops a database or table — data loss is permanent"},
	{regexp.MustCompile(`(?i)\btruncate\s+table\b`), "truncates a table — all rows will be deleted"},
	{regexp.MustCompile(`\b>\s*/etc/passwd\b`), "overwrites system password file"},
	{regexp.MustCompile(`\bcurl\b.*\|\s*(sudo\s+)?(ba)?sh\b`), "pipes remote content to shell — executes untrusted code"},
	{regexp.MustCompile(`\bwget\b.*\|\s*(sudo\s+)?(ba)?sh\b`), "pipes remote content to shell — executes untrusted code"},
}

func Check(command string) []Warning {
	var warnings []Warning
	for _, line := range strings.Split(command, "\n") {
		line = strings.TrimSpace(line)
		for _, p := range patterns {
			if p.re.MatchString(line) {
				warnings = append(warnings, Warning{
					Pattern: p.re.String(),
					Risk:    p.risk,
				})
				break
			}
		}
	}
	return warnings
}
