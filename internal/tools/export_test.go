package tools

// HoldSqliteRead runs f once per statement while that statement's read is
// still open, for a test outside the package that needs a writer to act
// under it. It returns the restore.
func HoldSqliteRead(f func()) (restore func()) {
	old := sqliteRowHook
	sqliteRowHook = f
	return func() { sqliteRowHook = old }
}
