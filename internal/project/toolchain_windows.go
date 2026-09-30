package project

import "io/fs"

// executableNames are the file names a lookup of name tries: Windows finds
// a program by its extension, not by a bit.
func executableNames(name string) []string {
	return []string{name, name + ".exe", name + ".cmd", name + ".bat"}
}

func executable(fs.FileInfo) bool { return true }
