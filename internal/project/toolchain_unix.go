//go:build !windows

package project

import "io/fs"

// executableNames are the file names a lookup of name tries.
func executableNames(name string) []string { return []string{name} }

// executable reports an execute bit for anyone: a shell runs a file whose
// bit is set for it, and the owner's is the one that is set in practice.
func executable(info fs.FileInfo) bool { return info.Mode().Perm()&0o111 != 0 }
