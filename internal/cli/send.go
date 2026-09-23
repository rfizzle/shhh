package cli

// A line from one session to another.
//
// Every terminal session listens on a socket of its own under the state
// directory, named for its process, and takes one thing there: a line of
// text another session — or a script standing in for one — sends it with
// `shhh send`. The line reaches the turn as a steer does and carries no
// authority at all: it cannot answer a card, run a command or change a
// setting, and what becomes of it is sessions.inbound's to say on the
// receiving side.
// See docs/capabilities/sessions-and-memory.md#a-session-can-hand-another-a-line.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/rfizzle/shhh/internal/cli/report"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/rpc"
	"github.com/rfizzle/shhh/internal/storage"
	"github.com/rfizzle/shhh/internal/ui/chat"
)

// inboxHandOff bounds each half of handing a line to the session's screen:
// joining its queue, and hearing what became of it. The screen answers
// between frames, so a session that has not answered in this long is one
// whose screen is not running. Both halves together stay inside the inbox's
// own connection deadline, with room to read the line and write the answer.
const inboxHandOff = 5 * time.Second

// inboxPath is where the session running as pid listens. The directory is
// the state directory's rather than the temporary one's, because the name is
// the whole of how a sender finds it: the record says which process holds a
// slot, and the process says where its socket is.
func inboxPath(pid int) (string, error) {
	dir, err := storage.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "inbox", strconv.Itoa(pid)+".sock"), nil
}

// openInbox starts this session's listener and returns the lines it takes,
// with the call that closes it. A session that cannot bind says so once — on
// stderr, before the screen opens — and takes nothing: the
// session is whole without it, and a line sent to it is told the session is
// not listening.
func openInbox(ctx context.Context, policy string) (<-chan chat.InboundLine, func()) {
	path, err := inboxPath(os.Getpid())
	if err == nil {
		err = os.MkdirAll(filepath.Dir(path), 0o700)
	}
	var l net.Listener
	if err == nil {
		// A file already at this name was left by a process that held this
		// pid before and did not close its socket: only one process holds a
		// pid at a time, so it cannot be anybody's live inbox.
		_ = os.Remove(path)
		l, err = net.Listen("unix", path)
	}
	if err != nil {
		note := "this session takes no lines from other sessions: its socket could not be opened (" + err.Error() + ")"
		_ = report.Fprintln(os.Stderr, report.Row{State: report.Warn, Subject: note})
		return nil, func() {}
	}
	_ = os.Chmod(path, 0o600)
	lines := make(chan chat.InboundLine, 8)
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	inbox := rpc.NewInbox(func(p rpc.SendParams) (string, error) {
		return handInbound(ctx, lines, policy, p)
	})
	go func() {
		defer close(done)
		_ = inbox.Serve(ctx, l)
	}()
	return lines, func() {
		cancel()
		<-done
		_ = os.Remove(path)
	}
}

// handInbound gives one line to the session's screen and reports what it
// made of it. A refusal the settings already state is answered here, without
// the screen: the line is taken by nothing, so nothing needs to see it.
func handInbound(ctx context.Context, lines chan<- chat.InboundLine, policy string, p rpc.SendParams) (string, error) {
	if strings.EqualFold(strings.TrimSpace(policy), chat.InboundRefuse) {
		return "", rpc.ErrRefused
	}
	taken := make(chan string, 1)
	select {
	case lines <- chat.InboundLine{From: p.From, Text: p.Text, Taken: taken}:
	case <-ctx.Done():
		return "", errors.New("the session is closing")
	case <-time.After(inboxHandOff):
		return "", errors.New("the session is not taking lines right now")
	}
	select {
	case word := <-taken:
		if word == "" {
			return "", rpc.ErrRefused
		}
		return word, nil
	case <-ctx.Done():
		return "", errors.New("the session closed before it said what became of the line")
	case <-time.After(inboxHandOff):
		return "", errors.New("the line was handed over and the session has not said what became of it")
	}
}

// sendingSlot is who a line is from where the sender did not say: the one
// session saving from the checkout `shhh send` was run in, which is the
// session a script in that worktree is speaking for. Two sessions in one
// checkout, or none, leave it unnamed rather than guessed.
func sendingSlot(db *storage.DB, now time.Time, to string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	root := project.Root(cwd)
	sessions, err := runningSessions(db, now)
	if err != nil {
		return ""
	}
	from := ""
	for _, s := range sessions {
		if s.Slot == "" || s.Slot == to || s.Root != root {
			continue
		}
		if from != "" {
			return ""
		}
		from = s.Slot
	}
	return from
}

// newSendCmd is `shhh send`.
func newSendCmd() *cobra.Command {
	var from string
	cmd := &cobra.Command{
		Use:   "send <slot> <text>",
		Short: "Hand a running session a line it reads at its next boundary",
		Long: "Hand the session saving to <slot> one line of text, which it reads as a steer from another session: " +
			"at its next round boundary if it is working, as its next instruction if it is idle. " +
			"The line is text and nothing else — never an approval, a command or a setting — and the receiving " +
			"session's sessions.inbound decides whether it is passed on, held for its person, or refused. " +
			"`shhh sessions` lists the slots.",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			to, text := args[0], strings.Join(args[1:], " ")
			db, err := openStore()
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer db.Close()
			now := time.Now()
			pid, ok, err := db.LiveSessionPID(to, now)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("no session running on this machine saves to %q; `shhh sessions` lists them", to)
			}
			if !cmd.Flags().Changed("from") {
				from = sendingSlot(db, now, to)
			}
			path, err := inboxPath(pid)
			if err != nil {
				return err
			}
			conn, err := net.DialTimeout("unix", path, 2*time.Second)
			if err != nil {
				return fmt.Errorf("session %q is not taking lines: %w", to, err)
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(2*inboxHandOff + 2*time.Second))
			res, err := rpc.Send(conn, rpc.SendParams{From: from, To: to, Text: text})
			var rerr *rpc.Error
			if errors.As(err, &rerr) && rerr.Code == rpc.CodeRefused {
				return fmt.Errorf("session %q refuses lines from other sessions (sessions.inbound = refuse)", to)
			}
			if err != nil {
				return fmt.Errorf("session %q: %w", to, err)
			}
			row := report.Done("sent to", to)
			if res.Taken == rpc.TakenHeld {
				row = report.Done("held by", to)
				row.Detail = "it waits on a card for the person there"
			}
			return report.Fprintln(cmd.OutOrStdout(), row)
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "the slot the line is from; by default the one session saving from this checkout, if there is exactly one")
	return cmd
}
