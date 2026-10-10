//go:build !windows

package sandbox

// The second leg of a container command's stop.
//
// The first leg is the stream: the client on the Mac lets go of its stdin
// and the helper at the far end stops the group. That leg depends on the
// engine passing the hang-up on, and on the helper being there to act on it
// — and an engine forwards no signal to an exec, may hold the stream open
// after the client has gone, and kills the exec's one process (the helper,
// not the group it leads) when the client disconnects. So the helper also
// records the group it started, under a token the host handed it, and a
// second exec of the helper with `--stop=<token>` stops that group with the
// same sequence. The host makes the second exec only when the first leg was
// not enough: the client was still there when the far end's grace was up.
// See docs/capabilities/containment.md#a-cancelled-command-takes-its-children-with-it.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/rfizzle/shhh/internal/runner"
)

// groupDir is where the helper records its groups inside a container: /tmp
// is there in every image a session runs, the sandbox's own base included,
// and is writable by whichever user the image runs as. execDirEnv moves it,
// for the tests that run the helper on a host; nothing in a container sets
// it, and an exec that did would only hide its own command from the stop.
const (
	groupDir   = "/tmp/shhh-exec"
	execDirEnv = "SHHH_SANDBOX_EXEC_DIR"
)

func recordDir() string {
	if d := os.Getenv(execDirEnv); d != "" {
		return d
	}
	return groupDir
}

// groupRecord is what is kept for one running command: its group id, and
// the leader's start time where the system says it, so that a number the
// machine has since handed to something else is not taken for the command.
type groupRecord struct {
	pgid  int
	start string
}

// recordGroup writes the group's record under token and returns what
// removes it. A record that cannot be written leaves the command running
// with only the stream to stop it: refusing to run it would be worse.
func recordGroup(token string, pgid int) func() {
	dir := recordDir()
	path := filepath.Join(dir, token)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		debugf("no record of the group: %v", err)
		return nil
	}
	tmp := path + ".new"
	body := strconv.Itoa(pgid) + " " + procStart(pgid) + "\n"
	if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
		debugf("no record of the group: %v", err)
		return nil
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		debugf("no record of the group: %v", err)
		return nil
	}
	debugf("group %d recorded", pgid)
	return func() { _ = os.Remove(path) }
}

// readRecord reads the record under token.
func readRecord(token string) (groupRecord, error) {
	data, err := os.ReadFile(filepath.Join(recordDir(), token))
	if err != nil {
		return groupRecord{}, err
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return groupRecord{}, errors.New("an empty record")
	}
	pgid, err := strconv.Atoi(fields[0])
	if err != nil || pgid <= 1 {
		return groupRecord{}, fmt.Errorf("a record with no group in it: %q", data)
	}
	r := groupRecord{pgid: pgid}
	if len(fields) > 1 {
		r.start = fields[1]
	}
	return r, nil
}

// ours reports whether the recorded group is still there and still the
// command's. A group lives while any member does, and while it lives its
// number is not handed out again, so a group that answers and whose leader
// is either gone or is the process that was recorded is the command's.
func (r groupRecord) ours() bool {
	if err := syscall.Kill(-r.pgid, 0); err != nil && !errors.Is(err, syscall.EPERM) {
		return false
	}
	if now := procStart(r.pgid); now != "" && r.start != "" && now != r.start {
		return false
	}
	return true
}

// procStart is the process's start time in clock ticks since boot, from
// /proc, or "" where it is gone or the system has no /proc to ask.
func procStart(pid int) string {
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return ""
	}
	i := strings.LastIndexByte(string(stat), ')')
	if i < 0 {
		return ""
	}
	// After the command name: state is the first field and starttime the
	// twentieth (proc(5)'s field 22).
	fields := strings.Fields(string(stat[i+1:]))
	if len(fields) < 20 {
		return ""
	}
	return fields[19]
}

// stopRecorded is `--stop=<token>`: the recorded group gets the runner's
// own stop sequence. A token with no record, or a record whose group has
// ended, is no such command, and nothing is signalled: the number may
// already belong to something else.
func stopRecorded(token string) int {
	r, err := readRecord(token)
	if err == nil && !r.ours() {
		_ = os.Remove(filepath.Join(recordDir(), token))
		err = errors.New("its group has ended")
	}
	if err != nil {
		debugf("stop %s: %v", token, err)
		fmt.Fprintln(os.Stderr, "shhh: sandbox exec: no such command")
		return 1
	}
	debugf("stopping group %d", r.pgid)
	runner.StopRecordedGroup(r.pgid, r.ours)
	return 0
}
