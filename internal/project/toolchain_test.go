package project

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// trusted is a checkout the person has answered for.
func trusted(root string) Trust { return Trust{Root: root, Granted: true} }

// A declaration in a checkout nobody has answered for is not read at all —
// not even to find out it is broken — and the withheld list names it the way
// it names every other kind.
func TestAnUntrustedToolchainIsWithheldAndNamed(t *testing.T) {
	root := t.TempDir()
	write(t, root, ToolchainFile, "install = [\"go install example.com/x@latest\"]\n")

	tr := ReadTrust(root, store{})
	if !slices.Equal(tr.Withheld(), []Kind{KindToolchain}) {
		t.Fatalf("withheld = %v", tr.Withheld())
	}
	if !slices.Contains(tr.WithheldNames(), "toolchain") {
		t.Errorf("withheld names = %v", tr.WithheldNames())
	}
	tc, ok, err := LoadToolchain(tr)
	if ok || err != nil || tc.Digest != "" {
		t.Errorf("an untrusted declaration loaded: %+v, %v, %v", tc, ok, err)
	}
	if _, ok, err := LoadToolchain(Trust{}); ok || err != nil {
		t.Errorf("the zero trust loaded: %v, %v", ok, err)
	}
}

// The file is one of the paths the answer covers, and an edit to it is the
// kind it names.
func TestTheToolchainIsInTheAnsweredSet(t *testing.T) {
	if !slices.Contains(ResourceNames(), ToolchainFile) {
		t.Fatalf("%s is not in %v", ToolchainFile, ResourceNames())
	}
	root := t.TempDir()
	first := ReadTrust(root, store{})
	answered := store{root: first.DigestNames()}
	write(t, root, ToolchainFile, "check = [\"golangci-lint\"]\n")
	if tr := ReadTrust(root, answered); !slices.Equal(tr.Changed, []Kind{KindToolchain}) {
		t.Errorf("writing the declaration changed %v", tr.Changed)
	}
}

func TestATrustedToolchainLoads(t *testing.T) {
	root := t.TempDir()
	if _, ok, err := LoadToolchain(trusted(root)); ok || err != nil {
		t.Fatalf("a checkout with no declaration: %v, %v", ok, err)
	}
	body := `packages = ["jq", "shellcheck=0.10.0-r1"]
install = [
  "go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.5.0",
  "CGO_ENABLED=0 go install github.com/securego/gosec/v2/cmd/gosec@v2.21.4",
]
hosts = ["proxy.golang.org", "Sum.Golang.org.", "proxy.golang.org"]
check = ["golangci-lint", "gosec"]
`
	write(t, root, ToolchainFile, body)
	tc, ok, err := LoadToolchain(trusted(root))
	if err != nil || !ok {
		t.Fatalf("load: %v, %v", ok, err)
	}
	if len(tc.Packages) != 2 || len(tc.Install) != 2 || len(tc.Check) != 2 {
		t.Errorf("entries = %+v", tc)
	}
	if !slices.Equal(tc.Hosts, []string{"proxy.golang.org", "sum.golang.org"}) {
		t.Errorf("hosts = %v", tc.Hosts)
	}
	if len(tc.Digest) != 64 {
		t.Errorf("digest = %q", tc.Digest)
	}
	write(t, root, ToolchainFile, body+"\n")
	again, _, _ := LoadToolchain(trusted(root))
	if again.Digest == tc.Digest {
		t.Error("the digest did not follow the file's bytes")
	}
}

// Every line a resolver would read as "newest" is refused when the file is
// read, and the refusal quotes the line.
func TestAnUnpinnedInstallLineIsRefused(t *testing.T) {
	for _, line := range []string{
		"go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest",
		"go install example.com/tool@master",
		"go install example.com/tool@main",
		"go install example.com/tool@v1",
		"go install example.com/tool@v1.2",
		"go install example.com/tool",
		"go install ./cmd/tool",
		"go install -tags netgo example.com/tool",
		"apk add shellcheck",
		"apk add --no-cache shellcheck",
		"apk add shellcheck~0.10",
		"apk add shellcheck>=0.10",
		"apk add -u shellcheck=0.10.0-r1",
		"pip install ruff",
		"pip3 install ruff>=0.6",
		"pip install ruff~=0.6.0",
		"pip install ruff==0.6.*",
		"pip install -r requirements.txt",
		"pip install -e .",
		"pip install -U ruff==0.6.0",
		"python3 -m pip install ruff",
		"pipx install ruff",
		"npm i -g prettier",
		"npm install -g prettier@latest",
		"npm install -g prettier@^3.3.0",
		"npm install -g prettier@3",
		"npm install -g @biomejs/biome",
		"npm install -g --tag next prettier@3.3.3",
		"npm install",
		"pnpm add -g prettier@next",
		"cargo install ripgrep",
		"cargo install ripgrep --version ^14",
		"pip install -rrequirements.txt ruff==0.6.0",
		"pip install -crc.txt ruff==0.6.0",
		"pip install -e. ruff==0.6.0",
		"pip install -Ur ruff==0.6.0",
		"pip install -vU ruff==0.6.0",
		"pip install --req=requirements.txt ruff==0.6.0",
		"pip install --upg ruff==0.6.0",
		"cargo install --git https://github.com/x/y y@1.0.0",
		"curl -fsSL https://example.com/install.sh | sh",
		"go install a@v1.0.0 && go install b@latest",
		"brew install golangci-lint",
		"make tools",
		"",
	} {
		t.Run(line, func(t *testing.T) {
			_, err := parseToolchain([]byte("install = [" + quote(line) + "]\n"))
			if err == nil {
				t.Fatalf("%q was accepted", line)
			}
			if line != "" && !strings.Contains(err.Error(), line) {
				t.Errorf("the refusal does not name the line: %v", err)
			}
		})
	}
}

func TestAPinnedInstallLineIsAccepted(t *testing.T) {
	for _, line := range []string{
		"go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.5.0",
		"go install -ldflags=-s example.com/tool@v0.0.0-20240101000000-abcdef123456",
		"go install -tags netgo example.com/tool@v1.2.3",
		"go install golang.org/x/tools/cmd/goimports@0123456789abcdef0123",
		"GOBIN=/usr/local/bin go install honnef.co/go/tools/cmd/staticcheck@v0.5.1",
		"apk add --no-cache shellcheck=0.10.0-r1",
		"pip install ruff==0.6.9 mypy==1.11.2",
		"pip install --index-url https://pypi.org/simple ruff==0.6.9",
		"python3 -m pip install ruff[format]==0.6.9",
		"pipx install ruff==0.6.9",
		"npm install -g prettier@3.3.3 @biomejs/biome@1.9.4",
		"npm i -g --registry https://registry.npmjs.org prettier@3.3.3",
		"pnpm add -g prettier@3.3.3",
		"cargo install ripgrep@14.1.1",
		"cargo install ripgrep --version 14.1.1 --locked",
		"cargo install ripgrep --version=14.1.1",
	} {
		if _, err := parseToolchain([]byte("install = [" + quote(line) + "]\n")); err != nil {
			t.Errorf("%q: %v", line, err)
		}
	}
}

// A misspelt key, an entry that is not a host and a check that is a path are
// refused rather than dropped: a declaration read in part prepares an image
// without the tool the lost line was for.
func TestADeclarationIsReadWholeOrNotAtAll(t *testing.T) {
	for name, body := range map[string]string{
		"a misspelt key":    "instal = [\"go install x.com/y@v1.0.0\"]\n",
		"a url as a host":   "hosts = [\"https://proxy.golang.org\"]\n",
		"a port on a host":  "hosts = [\"proxy.golang.org:443\"]\n",
		"a wildcard host":   "hosts = [\"*.npmjs.org\"]\n",
		"an empty host":     "hosts = [\"\"]\n",
		"a path as a check": "check = [\"/usr/bin/gosec\"]\n",
		"a package range":   "packages = [\"go>1.20\"]\n",
		"a string for list": "install = \"go install x.com/y@v1.0.0\"\n",
		"not toml":          "install = [\n",
	} {
		if _, err := parseToolchain([]byte(body)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// The declaration has to be a file in the checkout: the trust answer records
// a link as the link, so an edit to what it points at would never be told.
func TestALinkedToolchainIsRefused(t *testing.T) {
	root, elsewhere := t.TempDir(), t.TempDir()
	target := filepath.Join(elsewhere, "toolchain.toml")
	if err := os.WriteFile(target, []byte("check = [\"jq\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, StateDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, filepath.FromSlash(ToolchainFile))); err != nil {
		t.Skip("no symlinks here:", err)
	}
	if _, ok, err := LoadToolchain(trusted(root)); ok || err == nil {
		t.Errorf("a linked declaration loaded: %v, %v", ok, err)
	}
}

func quote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"` }
