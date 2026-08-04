# The menu's filter beats bare leader runes

The action menu opens with a focused type-to-filter field, and while it is up
a bare printable rune goes to that field. It does **not** fire the rune's
Esc-leader action, even though the menu doubles as the shortcut cheat-sheet
and bare runes used to fire.

Two things force it. `leaderBindings` binds 21 of the 26 letters, so
honouring bare runes leaves almost no filter typeable — "switch branch"
would save the file on its first keystroke. And silently running an action
while a caret blinks in an input is the exact class of surprise the
no-`Ctrl+` rule exists to prevent.

The cheat-sheet role survives in three ways, all of them load-bearing:

- Every row still renders its `Esc s` hint, filtered or not, and drill-in
  picks carry the hint in their dimmed tag column.
- `Alt+<rune>` still fires the action. This is not a fallback: tmux and most
  terminals deliver a fast `Esc s` as exactly one `Alt+s` event, so the
  printed gesture keeps working with the menu up on the setups skiff targets.
- An already-armed leader window still beats the filter, via
  `leaderWindowIntercept`, for the terminals that send `Esc` and the rune as
  two separate events. That is the same precedence `handleKey` applies in the
  editor — an armed Esc window wins over typing into the buffer — so the
  filter is not a special case.

`Esc` is the menu's own key: it clears a non-empty filter, and closes the
menu on a second press. It deliberately does not re-arm the leader window —
dismissing a modal must never leave a live hotkey window that turns the
user's next keystroke into an action.

## Considered Options

**Bare runes keep firing, filter only after a modifier or a `/` prefix.**
Rejected: it puts the menu's primary interaction behind a prefix nobody
discovers, to preserve a gesture that the same terminals already deliver as
`Alt+<rune>` anyway.

**Filter only matches unbound letters.** Rejected as unimplementable in a way
a user could predict: the typeable alphabet would depend on the leader table,
and adding a binding would silently break an existing filter word.

## Consequences

Do not "restore" bare-rune leader dispatch in `handleMenuKey`. Adding a
leader binding no longer has any effect on what the menu can filter, which is
what makes both tables free to grow. `handleMenuKey`'s Alt block and the
`leaderWindowIntercept` call are the two halves that keep the printed hints
honest — removing either one makes the menu's own cheat-sheet a lie.
