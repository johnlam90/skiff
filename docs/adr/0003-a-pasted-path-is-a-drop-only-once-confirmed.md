# A pasted path is a drop only once confirmed

Rex, Herdr and Ghostty do not tell a terminal program that a file was
dropped on it. They paste the file's path. So skiff reads a paste that is
nothing but existing paths as a file drop — and then **waits**: it raises
the placement bar and copies nothing until the user clicks a folder or
`[ Copy ]`. Left alone, the bar's answer is "this was text".

Two properties of that paste force the wait.

It carries no pointer position. The editor knows *what* was dropped and
not *where* the user let go, so there is no folder to copy into yet. A
click is the first event that does carry a position — and the pointer is
still resting on the row the user dropped on, so the gesture is "drop,
click".

And it is byte-identical to the same path pasted as text. A user who
pastes `/etc/hosts` — or the path of a whole directory — into a script
would otherwise watch skiff copy it into the repository. That failure is
worse than not having the feature, so it is the one thing the design is
built to make impossible.

Waiting is not enough on its own: the bar may be up in front of someone
who pasted text and never looked down, and whatever they do next must not
mean "copy". So the bar is keyed for the person who is not reading it:

- **Enter presses the focused button, and the focus starts on
  `[ As text ]`.** A reflex Enter after pasting a path puts the path in the
  buffer. The same reasoning starts the disk-conflict prompt on "Keep
  mine".
- **Typing means "as text".** The first character answers the bar and is
  then typed, so a sentence continued after the paste loses nothing.
- **A copy takes a deliberate step**: a click on a folder row or on
  `[ Copy ]`, or the keyboard's way there — Tab onto the button, or the
  arrows, which choose a folder and so cannot be anything but placing a
  drop.

(The first version copied on Enter and swallowed every key but `t`; review
showed "paste a path, press Enter" creating a file, which is this ADR's own
anti-goal one keystroke away.)

The pasted-path route stays on in every terminal, always. Where a terminal
also delivers kitty's drag-and-drop protocol (OSC 72), skiff uses those
events too — live hover, exact row, file contents over SSH — but it
registers for them without asking first and never treats the handshake as
proof of anything.

## Considered Options

**Copy on sight into the active folder.** Rejected: it is the anti-goal
above, and it lands the file in whichever folder was highlighted rather
than the one the user pointed at.

**Infer the pointer from the first motion report after the paste.** The
pointer has not moved, so the first report names the drop row. Rejected
as the decision rule: it depends on the user's hand twitching within some
window, and a rule that copies or types depending on a twitch is not one
anyone can learn. The bar uses that same report honestly instead — to
light the row, not to decide.

**Ask the terminal whether it speaks OSC 72, and pick a route.**
Rejected: Rex 0.1.0-preview.20 answers the `t=q` handshake and has no way
to deliver a drop. A client that believed the answer would switch the
pasted-path route off on the one terminal that needs it.

## Consequences

Do not "streamline" the bar away, and do not add a setting that copies
without it. Do not send `t=q`, and do not gate the pasted-path route on
anything the terminal says about itself.

Do not make Enter copy by default, and do not swallow printable keys in
the bar.

A paste that arrives while the bar is up is buffered whole and joins the
pending drop (`handleKey`'s `placeBarUp` branch): delivered rune by rune,
its first character would answer the bar "this was text" and the rest
would be typed into the buffer.

Nothing inside a bracketed paste is ever read as a drag-and-drop protocol
message (`dnd.Scanner` passes everything between the paste markers
through), and a protocol drop is honoured only after a hover the editor
accepted. Pasted bytes are content; they must not be able to make the
editor fetch and copy a file with no bar at all.

Only a paste headed for the editor is ever read as a drop. One that
arrives while a prompt or the find bar has the keyboard is text for that
field, as before.

Ghostty over plain SSH cannot work on this route: it pastes the laptop's
path, which the remote machine cannot read. Skiff says so (the bar's
"isn't on this machine" state) instead of typing the path into the open
file; the fix is a terminal that uploads (Rex, Herdr) or delivers OSC 72.
