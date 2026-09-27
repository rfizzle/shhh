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
`make docs-check` fails when the two drift. Two places in the prose are
marked *verify*: a default known from use, not from a page.

## The draft spends chords only

A key that is live while the draft can take text has to be a chord, because a
bare one is a letter in the sentence being typed ([a key is inert until its
surface holds the
keyboard](principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard)).
Enter and esc are the exceptions and there is no third, so every offer the
input frame makes comes out of the free set at the end of this page — which is
why that set is short, and why it is worth keeping accurate. The key list is
the offer that moved for this rule: it was `?` on an empty draft, which is a
false offer the moment somebody starts a sentence with a question mark, and it
is `ctrl+]` now.

An alt chord is the wrong answer for a key a reader reaches for when they are
lost. On the stock macOS terminals Option composes a character until the
profile is told to send the escape prefix, so an alt chord is dead there until
a box is ticked — which is why a Mac ships no alt chord at all
([below](#a-mac-ships-without-alt)), and why the key list is on a ctrl chord
everywhere. Where an alt chord is bound on a Mac anyway, because a keymap
file put one there, `shhh doctor`'s keys row is what reads the setting and
says which box, and the surfaces that offer the chord name that row beside
it.

### The ten a transcript row spends

The offers a transcript row makes are the largest single claim on the free
set, because there are ten of them and each is drawn beside a live draft
([the row's side of it](surfaces.md#the-turns-close)). On Linux and Windows
they are all on alt: every ctrl letter a terminal delivers is spent or the
line editor's, and the free set at the end of this page is function keys and
modified navigation keys, which ten acts named after words do not come out
of. A Mac ships them on that free set instead
([below](#a-mac-ships-without-alt)). The first row in a session to offer an
alt chord names the doctor's Option row beside it.

A turn's review is not among them. It is the changed-files row's own open —
clicked, or selected and opened with enter — because that is the gesture that
names the turn, and it gave back `alt+w`.

The table below is the Linux and Windows keyboard. On alt, the letter each
takes is the row's own where alt still had it. Five letters were already
spent, and this is where each replacement is recorded; the Mac's spelling of
each offer is the generated table [below](#a-mac-ships-without-alt):

| Offer | On the row | The chord | Why not the row's own letter |
|---|---|---|---|
| commit | `g` | `alt+g` | — (`ctrl+g` is the editor's, which is why this is on alt) |
| undo turn | `u` | `alt+z` | `alt+u` is the textarea's uppercase-word; `z` is undo everywhere else |
| try again | `r` | `alt+r` | — |
| continue from here | `c` | `alt+n` | `alt+c` is the textarea's capitalize-word; `n` is the next letter of the word |
| enter a new key | `e` | `alt+e` | — |
| switch provider | `p` | `alt+p` | — |
| more rounds | `+` | `alt+m` | the row draws the grant (`[+50]`), not a keystroke; `m` is *more* |
| let it run | `!` | `alt+x` | no mnemonic was left: `u`, `c` and `l` are the textarea's, and `a`, `p`, `n`, `r` and `t` are spent above |
| reopen the item | `o` | `alt+o` | — |
| run the checks again | `t` | `alt+k` | `alt+t` is the reasoning level's alias; `k` is the letter of *checks* |

The four alt letters the textarea holds — `alt+b`, `alt+f`, `alt+d` and the
case chords `alt+c`, `alt+l`, `alt+u` — are left to it for the reason the
readline chords are: the draft is a readline-shaped editor and those keys
reach it, so binding one takes a shell user's muscle memory to open something
they did not ask for.

## A Mac ships without alt

The keyboard shhh ships is one keyboard per platform, and the difference is
exactly the alt chords. On Linux and Windows the terminal sends the escape
prefix for alt and every alt chord above arrives. On a Mac the two stock
terminals compose a character for Option until a profile setting is ticked,
so the same chords would be fifteen offers that do nothing on the desktop
shhh is most often run on — the `alt+t` alias and the agent family among
them, which is how this was found.

So a Mac ships the same acts on the free set: the function row, which every
terminal delivers with nothing set (a laptop's top row sends them with the
`fn` key held). Plain F keys carry the offers a reader meets most — the turn's
close on `f2`–`f4`, the recovery rows on `f5`–`f8` with try again on `f5`,
the key that has meant reload everywhere, the round-limit pause on `f9`, the
agent manager on `f12` — and shift on the same row carries the rest. The
reasoning level keeps `ctrl+t` and drops its alt alias rather than moving it.
Every other key is the same on both, so the two are one register with a
different spelling in fifteen places, and every hint reads whichever this
machine ships. The table below is written from the Mac's table in the code:

<!-- BEGIN generated platform keys — written by `make docs` from the Mac's table in internal/ui/keys/platform.go; edit the table, not this. -->

| Key | Does | Linux and Windows | A Mac |
|---|---|---|---|
| `draft.reasoning` | cycle the reasoning level | `ctrl+t`, `alt+t` | `ctrl+t` |
| `draft.open_paste` | open the staged paste | `alt+v` | `shift+f12` |
| `draft.agents` | the agent manager | `alt+a` | `f12` |
| `draft.next_agent` | the next session in the rail's map | `alt+]` | `shift+f8` |
| `draft.prev_agent` | the previous one | `alt+[` | `shift+f7` |
| `rowchord.undo` | undo turn | `alt+z` | `f2` |
| `rowchord.retry` | try again | `alt+r` | `f5` |
| `rowchord.continue` | continue from here | `alt+n` | `f6` |
| `rowchord.key` | enter a new key | `alt+e` | `f7` |
| `rowchord.provider` | switch provider | `alt+p` | `f8` |
| `rowchord.rounds` | more rounds | `alt+m` | `f9` |
| `rowchord.uncap` | let it run | `alt+x` | `shift+f9` |
| `rowchord.reopen` | reopen the item | `alt+o` | `shift+f2` |
| `rowchord.commit` | commit | `alt+g` | `f3` |
| `rowchord.rerun` | run the checks again | `alt+k` | `f4` |

<!-- END generated platform keys -->

The Mac's keyboard is what a keymap file is applied over on a Mac, and what
`shhh keys` measures a moved key against, so a Mac with no file lists nothing
as moved. A file may put a key back on alt — the rules are the same five on
both keyboards — and then the doctor's keys row and the first row offering
that chord name the Option setting, because the chord is the person's own and
the setting is what it needs. `SHHH_KEYS_PLATFORM=linux` in the environment
runs the Linux keyboard on a Mac whose Option key already sends the escape
prefix, and it is what the scene harness sets so a capture reads the same on
every host. The shift+F spellings are the part of the table not yet pressed
at Terminal.app's default profile (*verify*); iTerm2 and the Linux terminals
report them as xterm does.

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

The Option key on macOS is the one row the table cannot settle from a page,
so it was settled at a keyboard. At Terminal.app's default profile on
2026-09-06, `alt+a`, `alt+[`, `alt+]` and `alt+t` typed `å`, `“`, `‘` and
`†`: "Use Option as Meta key" is off until a person ticks it, and every alt
chord shhh binds — the agent family and the `alt+t` alias — is dead there
until they do. iTerm2's Left Option "Esc+" is believed to be off the same
way (*verify* — its default profile has not been pressed yet).

On Linux and Windows the chords stay on alt: every ctrl letter the terminal
delivers is spent or the line editor's, the free set is function keys and
modified navigation keys, and alt costs nothing there. On a Mac they move to
that free set ([a Mac ships without alt](#a-mac-ships-without-alt)), and the
tick is only a question for a chord a keymap file put back on alt. What shhh
does about the tick: `shhh doctor` has a row for it, which on a Mac reads the setting from the profile Terminal.app
opens new windows with, or the profile the iTerm2 session is in, and says
which box turns it on — a profile that composes characters is a warning
naming the chords it costs, and the row is not checked at all off a Mac or
in a terminal whose preferences shhh does not read — and on a Mac with no
alt chord bound it has nothing to check and says so. The key list (`ctrl+]`,
and `/help`) names the same setting beside any alt chord it lists. iTerm2's "Meta" is
not the tick: it sets the eighth bit on the byte, which is not the
escape prefix a chord is, and the row says so.

Free chords, spelled the way the decoder spells them and so the way a
keymap file must: `ctrl+^`, the plain function keys `f2` … `f9` and `f12`,
and the modifier combinations on the arrow and navigation rows
the tables above do not name — `alt+pgup`, `alt+pgdown`, `alt+home`,
`alt+end`, `ctrl+home`, `ctrl+end`, `alt+shift+pgup`, `alt+shift+pgdown`,
`ctrl+alt+pgup`, `ctrl+alt+pgdown`. Every ctrl letter the terminal delivers
is spent or the line editor's (`ctrl+a`, `ctrl+e`, `ctrl+k`, `ctrl+u`,
`ctrl+w` stay with the textarea), which is why the agent manager went to
alt on Linux and Windows. `ctrl+]` left that set for the key list, which could not: it is the
door a lost reader opens, and a door behind the Option setting is a door
that is shut on the desktop where the setting is off. A Mac spends
`f2` … `f9` and `f12` from the same set, and shift on them.

What is left on alt on Linux and Windows — the Linux table; a Mac ships no
alt chord of its own — after the ten a
transcript row spends, the family the agent manager took, the staged paste
and the reasoning level's alias: `alt+h`, `alt+i`, `alt+j`, `alt+q`, `alt+s`,
`alt+w`, `alt+y`.
The six the textarea holds and `alt+0` … `alt+9`, which GNOME Terminal
switches tabs with, are not among them.

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
