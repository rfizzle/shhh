# Reserved keys

Chords the desktop, the stock terminal, a multiplexer or the line discipline
take before shhh sees them, or that arrive as some other key. A chord on this
page is not a chord: a hint that offers it is a false offer on at least one
platform shhh runs on, so the register may not spend it and a keymap file
that names one is refused
([the keymap file](../capabilities/configuration.md#the-keymap-file)).

Inventory of 2026-09-05, read from the vendors' own lists (sources at the
end). The tables are written by `make docs` from the table in
`internal/ui/keys/reserved.go`, which is the declaration the register's test
and the keymap file's refusal both read; this page is it printed, and
`make docs-check` fails when the two drift. One row of the prose is marked
*verify*: a default known from use, not from a page.

## What the encoding can carry

Before the list, the constraint that shapes it. shhh asks every terminal for
the enhanced keyboard protocols — xterm's modifyOtherKeys and the Kitty
keyboard protocol, both pushed on every render by the toolkit
(`internal/ui/chat/newline.go`). A terminal that grants them reports a
modified letter, enter, tab or space as itself: `ctrl+shift+t`,
`ctrl+enter` and `shift+enter` arrive named. Kitty, Ghostty, WezTerm, foot,
Alacritty and recent iTerm2 grant them. Terminal.app grants neither,
Windows Terminal grants neither (its own extended encoding is not one the
toolkit asks for), and tmux passes them through only when told to
(`extended-keys`). So a chord that exists only under the enhanced protocol
is a false offer on three of the terminals shhh is most often run in, and
the register's rule is that a shipped chord works in the legacy encoding:

- **A modifier on an arrow, a function key or a navigation key** (`pgup`,
  `pgdn`, `home`, `end`, `insert`, `delete`) is reported with any combination
  of shift, alt and ctrl by every terminal. Three-key chords that ship live
  here.
- **A modifier on a letter** is ctrl *or* alt in the legacy encoding, never
  both with shift: `ctrl+shift+x` arrives as `ctrl+x`, and `ctrl+x` for the
  letters `h`, `i`, `j`, `m`, `[` arrives as backspace, tab, newline, enter
  and esc.
- **A modifier on enter, tab or space** mostly does not arrive there:
  `shift+enter` and `ctrl+enter` are enter, `ctrl+space` is the NUL byte,
  which is why the newline has `ctrl+j` beside it and the rail names
  `shift+enter` as the key that works nearly everywhere rather than always.
- **Alt** arrives as an escape prefix — where the terminal sends one. The
  two stock macOS terminals do not by default (below).

So "we can always do three-key combos" is true of the arrow and function
rows everywhere, and true of the letters only on the terminals that grant
the protocols. The keyboard shhh ships stays in the legacy encoding, and so
does a keymap file: a shifted ctrl or alt letter is refused in a file too,
because the file travels with the reader to terminals that do not grant
the protocols, and nothing in it says which terminal it was written on.

## The inventory

<!-- BEGIN generated reserved keys — written by `make docs` from the table in internal/ui/keys/reserved.go; edit the table, not this. -->

### Tier A — the desktop takes it before the terminal sees it

| Chord | Taken by | On |
|---|---|---|
| `ctrl+up` | Mission Control | macOS |
| `ctrl+down` | the front app's windows | macOS |
| `ctrl+left`, `ctrl+right` | moving a space | macOS |
| `ctrl+space`, `ctrl+alt+space` | the input-source switcher | macOS |
| `ctrl+f2`, `ctrl+f3`, `ctrl+f4`, `ctrl+f5`, `ctrl+f6`, `ctrl+f7`, `ctrl+f8`, `ctrl+shift+f6` | keyboard focus to the menu bar, Dock, window, toolbar, floating window and status menus | macOS |
| `alt+tab`, `alt+esc` | the window switcher | Windows and GNOME |
| `alt+f4` | closing the window | Windows |
| `alt+space` | the window menu | Windows |
| `alt+f8` | the sign-in screen's password reveal | Windows |
| `ctrl+esc` | the Start menu | Windows |
| `ctrl+shift+esc` | Task Manager | Windows |
| `ctrl+alt+delete` | the security screen, or the power-off dialog | Windows and GNOME |
| `ctrl+alt+tab` | the app switcher, or focus to the top bar | Windows and GNOME |
| `shift+f10` | the context menu | Windows |
| `alt+f2` | the run-a-command window | GNOME |
| `ctrl+alt+up`, `ctrl+alt+down`, `ctrl+alt+left`, `ctrl+alt+right` | workspace switching | older GNOME and several other desktops |

### Tier B — the stock terminal takes it or retypes it

| Chord | Taken by | On |
|---|---|---|
| `ctrl+tab`, `ctrl+shift+tab` | the next and previous tab | Terminal.app and Windows Terminal |
| `ctrl+v`, `shift+insert` | paste | Windows Terminal |
| `ctrl+insert` | copy | Windows Terminal |
| `alt+enter` | full screen | Windows Terminal |
| `f11` | full screen | Windows Terminal and GNOME Terminal |
| `alt+up`, `alt+down`, `alt+left`, `alt+right` | pane focus | Windows Terminal |
| `alt+shift+up`, `alt+shift+down`, `alt+shift+left`, `alt+shift+right` | resizing the pane | Windows Terminal |
| `alt+shift+d`, `alt+shift+-`, `alt+shift++` | splitting the pane | Windows Terminal |
| `ctrl+shift+a`, `ctrl+shift+c`, `ctrl+shift+d`, `ctrl+shift+f`, `ctrl+shift+m`, `ctrl+shift+n`, `ctrl+shift+t`, `ctrl+shift+v`, `ctrl+shift+w`, `ctrl+shift+space`, `ctrl+shift+,` | select all, copy, duplicate the tab, find, mark mode, a new window, a new tab, paste, close the pane, the tab dropdown, the settings file | Windows Terminal |
| `ctrl+shift+up`, `ctrl+shift+down`, `ctrl+shift+pgup`, `ctrl+shift+pgdown`, `ctrl+shift+home`, `ctrl+shift+end` | scrolling the buffer | Windows Terminal |
| `ctrl+shift+1`, `ctrl+shift+2`, `ctrl+shift+3`, `ctrl+shift+4`, `ctrl+shift+5`, `ctrl+shift+6`, `ctrl+shift+7`, `ctrl+shift+8`, `ctrl+shift+9` | a new tab by profile | Windows Terminal |
| `ctrl+alt+1`, `ctrl+alt+2`, `ctrl+alt+3`, `ctrl+alt+4`, `ctrl+alt+5`, `ctrl+alt+6`, `ctrl+alt+7`, `ctrl+alt+8`, `ctrl+alt+,` | switching to a tab, and the defaults file | Windows Terminal |
| `ctrl+,`, `ctrl+0`, `ctrl++`, `ctrl+-` | settings and the font size | Windows Terminal and GNOME Terminal |
| `ctrl+shift+q`, `ctrl+shift+g`, `ctrl+shift+h`, `ctrl+shift+j` | close the window and the tab, find next and previous, clear the highlight | GNOME Terminal |
| `ctrl+pgup`, `ctrl+pgdown` | switching and moving tabs | GNOME Terminal |
| `alt+0`, `alt+1`, `alt+2`, `alt+3`, `alt+4`, `alt+5`, `alt+6`, `alt+7`, `alt+8`, `alt+9` | switching to a tab | GNOME Terminal |
| `ctrl+shift+left`, `ctrl+shift+right` | scrolling a line and jumping between commands | GNOME Terminal |
| `shift+pgup`, `shift+pgdown`, `shift+home`, `shift+end` | scrolling the buffer | GNOME Terminal, xterm and most Linux terminals |
| `f1`, `f10` | help and the menu bar | GNOME Terminal |

### Tier C — a multiplexer's prefix

| Chord | Taken by | On |
|---|---|---|
| `ctrl+b` | tmux's prefix | tmux |
| `ctrl+a` | screen's prefix | GNU screen |

### Tier D — the line discipline, and spellings that are another key

| Chord | Taken by | On |
|---|---|---|
| `ctrl+s`, `ctrl+q` | flow control | a cooked terminal, or an ssh hop that keeps it |
| `ctrl+\` | SIGQUIT | a cooked terminal |
| `ctrl+c`, `ctrl+z`, `ctrl+d` | interrupt, suspend and end of input | the line discipline |
| `ctrl+h`, `ctrl+i`, `ctrl+j`, `ctrl+m`, `ctrl+[`, `ctrl+@` | the same byte as backspace, tab, newline, enter, esc and NUL | every terminal |
| `shift+enter`, `ctrl+enter` | enter, without the enhanced keyboard protocol | Terminal.app, Windows Terminal and tmux |

### Kept on purpose

The chords the keyboard shhh ships spends although the list names them. Each is a code change beside a sentence, never a keymap file's decision.

| Chord | Why |
|---|---|
| `ctrl+c` | the interrupt is shhh's own in raw mode, and it does what the hand expects on every surface: cancel, back, then quit |
| `ctrl+d` | end of input is shhh's own in raw mode, and it quits the way a shell does |
| `ctrl+j` | the newline's own byte, declared as the cover for shift+enter |
| `ctrl+space` | its alias ctrl+y is the cover where the desktop takes it, which the register says beside it |
| `ctrl+v` | Windows Terminal's paste is what the chord means there, and pasted text arrives as a paste event |
| `ctrl+z` | the suspend is shhh's own in raw mode, and it hands the terminal back the way the shell would |
| `shift+enter` | the key nearly every terminal reports for a newline; ctrl+j covers the ones that do not |

<!-- END generated reserved keys -->

## What is left

The Option key on macOS is the one row the table cannot settle from a page:
Terminal.app's "Use Option as Meta key" and iTerm2's Left Option "Esc+" are
both off by default as far as use remembers (*verify*), and where they are,
every alt chord shhh binds — the agent family on `alt+a`, `alt+[`, `alt+]`,
and the `alt+t` alias — types a character instead. That is checked on a Mac,
not here.

Free chords, spelled the way the decoder spells them and so the way a
keymap file must: `ctrl+]`, `ctrl+^`, the plain function keys `f2` … `f9`
and `f12`, and the modifier combinations on the arrow and navigation rows
the tables above do not name — `alt+pgup`, `alt+pgdown`, `alt+home`,
`alt+end`, `ctrl+home`, `ctrl+end`, `alt+shift+pgup`, `alt+shift+pgdown`,
`ctrl+alt+pgup`, `ctrl+alt+pgdown`. Every ctrl letter the terminal delivers
is spent or the line editor's (`ctrl+a`, `ctrl+e`, `ctrl+k`, `ctrl+u`,
`ctrl+w` stay with the textarea), which is why the agent manager went to
alt.

## Sources

- Apple, Mac keyboard shortcuts: https://support.apple.com/en-us/102650
- Apple, Mission Control and Spaces: https://support.apple.com/guide/mac-help/open-windows-spaces-mission-control-mh35798/mac
- Apple, Terminal keyboard shortcuts: https://support.apple.com/guide/terminal/keyboard-shortcuts-trmlshtcts/mac
- Microsoft, keyboard shortcuts in Windows: https://support.microsoft.com/en-us/windows/keyboard-shortcuts-in-windows-dcc61a57-8ff0-cffe-9796-cb9706c75eec
- Microsoft, Windows Terminal actions and default bindings: https://learn.microsoft.com/en-us/windows/terminal/customize-settings/actions
- GNOME Terminal keyboard shortcuts: https://help.gnome.org/users/gnome-terminal/stable/adv-keyboard-shortcuts.html.en
- GNOME Shell keyboard shortcuts: https://help.gnome.org/users/gnome-help/stable/shell-keyboard-shortcuts.html.en
- iTerm2, Keys profile preferences (the Option key): https://iterm2.com/documentation-preferences-profiles-keys.html
- tmux(1) and screen(1) for the prefixes; termios(3) for the line discipline
