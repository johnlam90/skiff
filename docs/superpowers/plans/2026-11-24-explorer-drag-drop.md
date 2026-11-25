# Explorer Drag-and-Drop Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Files and folders dropped on Skiff are copied into a project folder the user picks, through a pasted-path route that works everywhere and an OSC 72 route where the terminal delivers it.

**Architecture:** Two inputs feed one drop model. A paste that is nothing but existing paths opens a placement bar (an app-side overlay); kitty's OSC 72 messages are lifted out of the tty stream below tcell and drive the same tree highlight. Both end in `filemanager.Import` on the existing `fileOp` job.

**Tech Stack:** Go 1.24, tcell v2.13.9, no new dependencies (stdlib `crypto/hmac`, `encoding/base64`, `net/url`).

**Spec:** `docs/superpowers/specs/2026-11-24-explorer-drag-drop-design.md`

> **Status, 2026-11-24: executed.** This file is the record of what was
> planned. The review that followed changed three things it still states
> the old way: the bar's keys (Enter no longer copies by default, and
> typing means "as text"), the scanner (no timeout on a message in
> progress — the `2s` constant below is gone), and the base64 unit
> (flushed per message, as kitty frames it). The spec's "Revised after
> review" notes and `docs/adr/0003-…` are current.

## Global Constraints

- No CGO, no new module dependencies, no `Ctrl+` shortcuts.
- Every new source file: the project header block (2026), a doc comment on every function, and a same-package `_test.go`.
- Tests use `t.TempDir()`; never the user's real config or state. Staging resolves through `$XDG_STATE_HOME`, which `TestMain` already redirects.
- Hot paint paths use `textdraw.Cell` / `Fill` / `DrawClipped`; every surface repaints its whole rect.
- Goroutine → loop hand-offs are an `asyncjob.Job` or `asyncjob.Notify`; no new event structs. `safeGo` is the only spawner.
- Nothing is ever overwritten: a taken name walks the " copy" ladder.
- The paste route stays on regardless of what OSC 72 says; Skiff never sends `t=q`.
- Must build for `linux`, `darwin`, `windows` (`GOOS=windows go build ./...`).
- Exact values: paste cap `64 KiB`; `256` paths; spring-load `700ms`; edge scroll `120ms`; capture cap `16 KiB`; capture idle `2s`; fetch idle `30s`; `100000` entries; depth `64`.

## Review Focus

- A second path paste while the bar is up joins the pending drop, and none of its characters act as the `t` key. → Task 6.
- Dropping a folder that contains the project is refused with a readable flash and copies nothing. → Tasks 2, 6.
- The target folder vanishing between the drop and the click flashes an error and leaves the editor usable. → Task 6.
- A truncated OSC 72 sequence neither eats later keystrokes for more than 2s nor releases its payload as typed text. → Tasks 3, 5.
- A hostile entry name (`..`, `a/b`, empty) from the terminal fails the drop and writes nothing outside staging. → Task 3.

---

### Task 1: `internal/droppath` — classify a paste

**Files:** Create `internal/droppath/droppath.go`, `internal/droppath/droppath_test.go`.

**Interfaces — Produces:**

```go
const MaxBytes = 64 << 10
const MaxPaths = 256
type Kind uint8
const ( Text Kind = iota; Paths; Foreign )
type Options struct {
	Stat    func(path string) error // nil or a permission error means "exists"
	Windows bool                    // backslash separates, never escapes
	Remote  bool                    // an SSH session: enables Foreign
}
func Classify(text string, o Options) (Kind, []string)
```

- [ ] Tests (`TestClassify_*`): backslash-escaped, single-quoted with `'\''`, double-quoted, `file://` with `%20`, several space-separated, one raw path per line, a raw path with spaces, a relative token → `Text`, a missing path → `Text`, permission error → `Paths`, over `MaxBytes`/`MaxPaths`/NUL → `Text`, Windows `"C:\a b\c.txt"`, duplicates collapse, `Foreign` only when `Remote` and every path sits under a desktop root and none exists.
- [ ] Run: `go test ./internal/droppath/` → FAIL (package missing).
- [ ] Implement. Readings in order: shell tokens, lines, whole text; the first whose paths are all absolute and all exist wins. `Foreign` considers the token reading only.
- [ ] Run: `go test ./internal/droppath/` → PASS.

### Task 2: `filemanager.Import`

**Files:** Create `internal/filemanager/import.go`, `internal/filemanager/import_test.go`.

**Interfaces — Produces:**

```go
var ErrMoveInsideRoot = errors.New("can't move a project entry in")
func (m *Manager) Import(srcs []string, dstDir string, move bool, progress func()) (Changeset, error)
```

Consumes the package's `uniqueDestPath`, `copyTree`, `moveEntry`, `isDirEntry`, `Within`, `abs`.

- [ ] Tests (`TestImport_*`): outside file lands; folder tree lands; clash → `x copy.txt`; several sources; folder containing the destination → `ErrIntoItself`; the root as a source → `ErrProjectRoot`; destination outside root → `ErrOutsideRoot`; destination not a folder; a missing source fails alone while the others land (Changeset lists them, error names it); a folder whose copy fails half-way leaves no partial destination; `move` renames and removes the source; `move` falls back across devices via the `rename` seam; `move` of an in-root source → `ErrMoveInsideRoot`; `progress` is called.
- [ ] Run → FAIL, implement, run → PASS (`go test ./internal/filemanager/`).

### Task 3: `internal/dnd` — the OSC 72 client

**Files:** Create under `internal/dnd/`: `msg.go`, `scanner.go`, `base64.go`, `urilist.go`, `machineid.go`, `machineid_darwin.go`, `machineid_linux.go`, `machineid_other.go`, `conn.go`, `fetch.go`, each with its `_test.go` (the three platform files share `machineid_test.go`).

**Interfaces — Produces:**

```go
// msg.go
const MIMEURIList = "text/uri-list"
const ( OpNone = 0; OpCopy = 1; OpMove = 2 )
type Msg struct {
	Type                          byte // the t key; 0 when absent
	More                          bool // m=1
	Op, X, Y, PX, PY              int  // o, x, y, X, Y
	HasOp, HasX, HasY, HasPX, HasPY bool
	Payload                       []byte
}
func Parse(body []byte) (Msg, error)
func Accept(mimes ...string) []byte        // t=a
func MachineIDMsg(id string) []byte        // t=a:x=1
func Stop() []byte                         // t=A
func Reply(op int, mimes ...string) []byte // t=m:o=
func RequestMIME(idx int) []byte           // t=r:x=
func RequestEntry(idx, sub int) []byte     // t=r:x=:y=
func RequestChild(handle, num int) []byte  // t=r:Y=:x=
func CloseDir(handle int) []byte           // t=r:Y=
func Finish(op int) []byte                 // t=r:o=
func MIMEs(payload []byte) []string
type RemoteError struct{ Name, Desc string }
func ErrorOf(payload []byte) *RemoteError

// scanner.go
const MaxBody = 16 << 10
type Scanner struct{ /* unexported */ }
func (s *Scanner) Feed(b []byte, emit func(body []byte)) (pass []byte)
func (s *Scanner) Held() bool
func (s *Scanner) Release() []byte
func (s *Scanner) Capturing() bool
func (s *Scanner) Abort()

// base64.go
type Decoder struct{ /* carry */ }
func (d *Decoder) Decode(chunk []byte) ([]byte, error)
func (d *Decoder) Close() ([]byte, error)

// urilist.go
type Entry struct { URI, Path string; File bool }
func ParseURIList(data []byte) []Entry
func EntryName(p string) (string, error)
func ValidName(name string) bool

// machineid.go
func HashMachineID(raw string) string // "1:" + hex(HMAC-SHA256("tty-dnd-protocol-machine-id", raw))
func MachineID() (string, error)

// conn.go
type Conn struct{ /* unexported */ }
func NewConn(out io.Writer, control func(Msg)) *Conn
func (c *Conn) Ingest(body []byte)
func (c *Conn) Send(seq []byte) error
func (c *Conn) Attach() *Inbox
type Inbox struct{ /* unexported */ }
func (in *Inbox) Send(seq []byte) error
func (in *Inbox) Recv(ctx context.Context, idle time.Duration) (Msg, error)
func (in *Inbox) Close()

// fetch.go
type Limits struct { MaxEntries, MaxDepth int; Idle time.Duration }
var DefaultLimits = Limits{MaxEntries: 100000, MaxDepth: 64, Idle: 30 * time.Second}
type Link interface {
	Send(seq []byte) error
	Recv(ctx context.Context, idle time.Duration) (Msg, error)
}
type Request struct {
	MIMEs    []string
	Exists   func(path string) bool
	Stage    func() (string, error)
	Limits   Limits
	Progress func()
}
type Result struct { Local, Staged []string; StageDir string }
var ErrNoFiles = errors.New("the drop carries no files")
func Fetch(ctx context.Context, l Link, req Request) (Result, error)
```

Wire rules the signatures do not fix:

- A body is `metadata;payload`, split at the first `;`. Metadata is colon-separated `key=value`; unknown keys are ignored.
- Scanner states: normal → introducer prefix (`ESC ] 7 2 ;`, held while undecided) → capture → done on `ESC \` or BEL. Any other C0 byte, or `ESC` followed by anything but `\`, abandons the capture and is reprocessed as plain input. Past `MaxBody` the scanner discards up to the terminator.
- `Conn.Ingest`: while a chain is open (`m=1` seen), the next message is its continuation and inherits the head's metadata. `m`/`M` are assembled (cap 1 MiB) and handed to `control`; `r`/`R` are streamed to the attached `Inbox`, or dropped when none is attached. A stream ends on a message with an empty payload and `m=0`.
- `Fetch` never sends `Finish`; the caller does, after importing.
- Remote flag: `X=1` on the first `text/uri-list` response. Entry kind from the first response to an entry request: `X` absent or `0` file, `1` symlink, anything else a directory handle with a NUL-separated listing.

- [ ] Tests, one group per file: `TestParse_*`/`TestEncode_*`; `TestScanner_*` (whole message, split across feeds at every byte, held prefix released, BEL terminator, control byte aborts and passes through, `ESC` + other aborts, oversize discards to the terminator, two messages in one feed, plain input untouched); `TestDecoder_*` (split mid-quantum, padded, unpadded tail, bad input); `TestParseURIList_*`, `TestValidName`; `TestHashMachineID` against a vector computed with `openssl dgst -sha256 -hmac`; `TestConn_*` (control assembled across a chain, data streamed with inherited keys, unattached data dropped, `Send` writes whole sequences); `TestFetch_*` against a scripted `Link` (local paths, remote file, remote tree breadth-first with handles closed, symlink, `t=R` reply, hostile name, silence → idle error, entry and depth limits, no `text/uri-list` → `ErrNoFiles`).
- [ ] Run → FAIL, implement, run → PASS (`go test ./internal/dnd/`).

### Task 4: tree drop target

**Files:** Modify `internal/filetree/filetree.go` (field), `render.go` (row + root style), `nav.go` (helpers); tests in `render_test.go`, `nav_test.go`.

**Interfaces — Produces:**

```go
// Tree field
DropTarget string // absolute folder path a pending drop would land in; "" = none
func (t *Tree) DirAt(localX, localY int) string          // folder a drop at this sidebar-local cell means
func (t *Tree) StepDir(path string, delta int) string    // folder rows in display order, root first, clamped
func (t *Tree) ShowDir(path string, viewH int)           // scroll path's row into the list area
func (t *Tree) NodeFor(path string) *Node                // loaded node for path, or nil
func (t *Tree) EdgeScroll(localY, h int) int             // -1 / 0 / +1: scroll owed by a hover on the list's edge row
```

- [ ] Tests: the drop-target row paints `theme.Selection` across its full width, and the root row does when the root is the target; `DirAt` for a folder row, a file row, the root row, the header and the blank area; `StepDir` over nested expanded folders and its clamps; `ShowDir` scrolls only when needed; `EdgeScroll` at both edges and with nothing beyond.
- [ ] Run → FAIL, implement, run → PASS (`go test ./internal/filetree/`).

### Task 5: `escTty` — lift OSC 72 out, serialize writes

**Files:** Modify `internal/app/esctty.go`, `internal/app/esctty_test.go`, `internal/app/app.go` (`newTerminalScreen`, `New`, `NewSingleFile` signatures).

**Interfaces — Produces:**

```go
const dndIdleTimeout = 2 * time.Second
func (t *escTty) SetSink(fn func(body []byte)) // receives each OSC 72 body; safe before or after the reader starts
func (t *escTty) Write(p []byte) (int, error)   // mutex-serialized
func newDoubleEscScreen() (tcell.Screen, *escTty, error)
func newTerminalScreen() (tcell.Screen, *escTty, error)
```

- [ ] Tests (alongside the existing ones, which must stay green): an OSC 72 message in a chunk reaches the sink and nothing reaches the reader; one split across chunks does the same; text before and after survives in order; a lone `ESC` still waits one `escMergeWindow` and a pair still becomes Alt+Esc; a stuck capture is abandoned after `dndIdleTimeout` and the next key is delivered; with no sink set messages are dropped without blocking.
- [ ] Run → FAIL, implement, run → PASS (`go test ./internal/app/ -run 'EscTty|DoubleEsc'`).

### Task 6: the paste route and the placement bar

**Files:** Create `internal/app/filedrop.go`, `internal/app/filedrop_test.go`. Modify `internal/app/keys.go` (`applyPaste`, the paste branch of `handleKey`), `fileclip.go` (`fileOpResult`, `handleFileOpDone`), `menudef.go` (File row), `app.go` (`drop` field), `menu_test.go`/`menudef_test.go` (pinned layout numbers).

**Interfaces — Consumes:** `droppath.Classify`, `(*Manager).Import`, the Task 4 tree helpers. **Produces:**

```go
type fileDrop struct{ /* phase, srcs, text, foreign, base, target, hover and spring state */ }
func (a *App) tryDropPaste(text string) bool            // true = the paste became a drop
func (a *App) startImport(srcs []string, dst string, move bool, after func(ok bool)) bool
func (a *App) dropFolderAt(x, y int) (string, *filetree.Node)
func (a *App) setDropTarget(dir string)
func (a *App) clearDrop()
func (a *App) menuImportPath()
func (a *App) importPathLabel() string
```

Copy fixed by the spec: bar line 1 `Copy <name|N items> into <folder>`; buttons `[ Copy ]`, `[ As text ]`, `[ Cancel ]`; hint `click a folder to choose`; foreign line `<name> isn't on this machine — this terminal pasted your computer's path`; flashes `Copying <name>…`, `Copied <name> into <folder>`, `Copied N items into <folder>`; menu label `Import path into <folder>…`.

- [ ] Tests (`TestFileDrop_*`): a pasted existing path opens the bar and inserts nothing; Enter copies into the active folder and the file is on disk and in the tree; a click on a folder row copies there; hovering a row moves `Tree.DropTarget`; `t` inserts the original text; Esc leaves disk and buffer alone; a paste with the find bar up is typed into it; single-file mode inserts text; a missing path inserts text; a second path paste joins the drop and never fires `t`; the target vanishing flashes an error; a folder containing the project flashes the refusal; spring-load expands after the timer's on-loop half runs and not when the hover moved; Up/Down step the target; the foreign state in an SSH session shows two buttons; the bar fits at `minWidth`×`minHeight`; `WantsMotion` is true while it is up; the ≡ row is reachable and imports.
- [ ] Run → FAIL, implement, run → PASS (`go test ./internal/app/`).

### Task 7: the protocol route

**Files:** Create `internal/app/dnd.go`, `internal/app/dnd_test.go`. Modify `internal/app/app.go` (`dnd` field, `New` wiring, `Close`).

**Interfaces — Consumes:** `dnd.*`, `(*escTty).SetSink`/`Write`, Task 6's drop helpers. **Produces:**

```go
func (a *App) attachDnd(out io.Writer) *dnd.Conn // registers unless multiplexed; returns the Conn whose Ingest is the tty sink
func (a *App) handleDnd(m dnd.Msg)               // on-loop: hover, leave, drop
func (a *App) stageRoot() (string, error)        // $XDG_STATE_HOME/skiff/drops
func multiplexed() bool                          // $TMUX, $STY or $ZELLIJ
```

- [ ] Tests (`TestDnd_*`, a `bytes.Buffer` as the tty): a hover over a tree row highlights it and writes `t=m:o=1;text/uri-list` once; a hover without `text/uri-list`, or with an overlay up, replies `o=0`; leaving clears the highlight; a drop of local paths copies them and then writes `t=r:o=1`; a remote drop served by a scripted terminal lands files and removes staging; a failing fetch writes `t=r:o=0`, flashes, and leaves nothing behind; registration is skipped when multiplexed; `Close` writes `t=A`.
- [ ] Run → FAIL, implement, run → PASS (`go test ./internal/app/`).

### Task 8: documentation

**Files:** Modify `CLAUDE.md` (architecture map, a design-pattern section), `CONTEXT.md` (Drop, Placement bar), `README.md`; create `docs/adr/0003-a-pasted-path-is-a-drop-only-once-confirmed.md`.

- [ ] Write them; `make lint` and `make test` green; `GOOS=windows go build ./...` and `GOOS=linux go build ./...` succeed.

### Task 9: verify against the real binary

- [ ] Build, run in a throwaway Rex session over a temp project, send a path through `block.event` paste, confirm the bar with `rex capture`, press Enter, confirm the file on disk and in the tree; repeat with a mouse click on a folder row.
- [ ] Drive the binary on a pty with a scripted OSC 72 terminal: hover, drop, remote transfer.
