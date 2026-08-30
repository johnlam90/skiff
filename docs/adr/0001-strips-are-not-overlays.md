# Strips are not overlays

Skiff has two kinds of chrome above the editor: **overlays** (menu, prompt,
confirm, pick, form, context, finder, diff, git log) float over the editor,
capture all input, and live on the overlay stack; **strips** (find bar,
project-find bar, leader strip) dock at the bottom, reflow the editor's
rect, capture keys, and deliberately let mouse actions pass through so you
can click and drag in the editor while they're open. Strips never sit on
the overlay stack — the stack's outside-click dismissal and input capture
would break the pass-through behavior, and the stack would have to learn
about layout reflow, which is `app`'s job.

## Considered Options

One uniform stack with per-surface policy flags (captures mouse?
dismisses on outside click? reflows layout?) was considered and rejected:
it drags layout concerns into the overlay module's interface and turns
the stack's simple contract into a configuration surface. Two kinds with
a shared widget vocabulary (strips reuse the overlay package's field
primitive) keeps both interfaces small.

## Consequences

The find bar's absence from mouse routing is intentional pass-through,
not a missing branch — an earlier architecture review misread it as a
bug. Do not "fix" it by routing mouse events to the find bar, and do not
re-propose putting strips on the stack.

This decision now has a shape in code: the `strip` interface and App's
one strip slot (`internal/app/strip.go`). What a strip is — the rows it
reserves, that it owns the keyboard, whether it consumes the mouse or
passes it through, how it is torn down — is that interface, and the
pass-through is `findStrip.handleMouse` answering false rather than an
absence anyone has to recognise. The interface is app-side on purpose:
reflowing the editor is layout's job, which is exactly the concern this
ADR refused to hand the overlay package.
