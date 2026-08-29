// =============================================================================
// File: internal/app/app.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-29
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Package app is the editor's top-level glue: it owns the tcell screen,
// the file tree, the open tabs, and the event loop. The drawing is split
// into four panels (sidebar / tab bar / editor body / status bar) and the
// mouse dispatcher routes presses, drags, and wheel events to whichever
// panel the cursor is over.
//
// The editor is mouse-first by design — there are no Ctrl-keyed shortcuts
// because they collide with terminal flow control (Ctrl-S/Q) and tmux/zellij
// prefixes. Instead, every action lives behind a click on the ≡ icon at
// the top-left of the tab bar, which opens a centered modal of actions.
package app

import (
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/johnlam90/skiff/internal/customactions"
	"github.com/johnlam90/skiff/internal/editor"
	"github.com/johnlam90/skiff/internal/filetree"
	"github.com/johnlam90/skiff/internal/finder"
	"github.com/johnlam90/skiff/internal/git"
	"github.com/johnlam90/skiff/internal/overlay"
	"github.com/johnlam90/skiff/internal/search"
	"github.com/johnlam90/skiff/internal/theme"
)

// Layout, behavior, and feel constants. Constants instead of config —
// the editor is opinionated by design.
const (
	defaultSidebarWidth = 30
	minSidebarWidth     = 18
	minEditorAfterDrag  = 40

	// minWidth / minHeight are the smallest terminal skiff will paint a
	// UI into; below either, draw() bails to drawTooSmall. Both are the
	// measured floor of the tallest and widest thing the editor can put
	// on screen, not round numbers — skiff's habitat is SSH from a
	// phone, where the soft keyboard eats half the display and the old
	// 50x24 refused a landscape phone (92x13) and an Android one (80x10)
	// outright.
	//
	// minHeight is set by the tallest FIXED-height prefab. Confirm,
	// Dirty and Prompt are all exactly 9 rows and, unlike Info, Pick,
	// Form and the action menu, have no windowed content to give back.
	// Nine of the ten rows go to the modal (Centered pins it at y=0) and
	// the tenth keeps the status bar visible underneath it — the bar is
	// where the modal's own outcome is reported, so covering it would
	// mean answering a prompt and never seeing the answer. Nothing else
	// comes close: the app's own chrome needs 3 rows (tab bar, one
	// editor row, status bar), 7 with the find bar and a full-height
	// flash strip up; the git panel's list survives on 5; the file
	// tree's on 4.
	//
	// minWidth is set by the widest fixed BUTTON row. Dirty's
	// Cancel / Discard / Save is 29 cells of label, and with one cell of
	// gap between neighbours and one of frame margin at each end it
	// cannot squeeze under 33. Above that arithmetic floor the binding
	// constraint is the action menu, whose frame is width-2: under about
	// 38 cells its label column drops below 25 and rows start reading as
	// unidentifiable prefixes. 40 clears both — and it is iPhone SE
	// portrait at the default font, the narrowest real terminal we
	// target.
	minWidth  = 40
	minHeight = 10

	// autoHideSidebarWidth is the terminal width below which the file
	// explorer collapses on its own. Not a taste number: it is exactly
	// the width at which the splitter's own clamp gives up — under
	// minSidebarWidth + minEditorAfterDrag the tree and the editor
	// cannot both keep their minimum, so resizeSidebar starts starving
	// the editor instead. A tmux split on a laptop crosses this
	// constantly, and a 39-column editor behind an 18-column tree is
	// worth less than no tree at all. See applyResponsiveSidebar.
	autoHideSidebarWidth = minSidebarWidth + minEditorAfterDrag
	statusFlashFor       = 3 * time.Second
	doubleClickWindow    = 500 * time.Millisecond
	doubleEscWindow      = 500 * time.Millisecond

	// menuEscWindow is the double-Esc window for opening the menu — much
	// wider than the leader's doubleEscWindow on purpose. Under tmux's
	// default escape-time, a fast Esc,Esc reaches tcell as ONE munched
	// Esc event and a slow pair arrives >500ms apart, so a 500ms window
	// made the double-tap nearly impossible to land. Two Escs inside
	// 1.2s is always a deliberate "give me the menu" gesture; a wider
	// window costs nothing because a lone armed Esc is already inert.
	menuEscWindow = 1200 * time.Millisecond
	wheelLines    = 3
	wheelCols     = 6 // horizontal step per WheelLeft/WheelRight event

	// modifierStickyWindow is how long a previously-seen Shift modifier
	// state is allowed to persist forward onto the next wheel event.
	// Some terminals (Zellij + macOS Terminal among them) emit the
	// Shift state as a separate ButtonNone+Shift event right before
	// firing the WheelUp/WheelDown without the modifier — so without
	// this carry-forward, shift+wheel reads as plain wheel. 250ms is
	// long enough to bridge the gap and short enough that releasing
	// Shift before scrolling reliably reverts to vertical scroll.
	modifierStickyWindow = 250 * time.Millisecond

	// treeRefreshInterval is how often the background goroutine kicks off
	// a file-tree reload so the sidebar stays in sync with on-disk changes
	// made by other tools (git, mv, another tmux pane, etc.). 10s feels
	// "fresh enough" while costing only a handful of ReadDir syscalls.
	treeRefreshInterval = 10 * time.Second

	// menuButtonWidth is how many cells the ≡ icon occupies at the top-left
	// of the tab bar. Tabs render starting just after it.
	menuButtonWidth = 4

	// modalWidth is the action modal's PREFERRED column count. Sized to
	// comfortably fit the longest dynamic label — "Rename folder
	// (subdir/)" with a folder name up to maxLabelSuffix runes — plus the
	// leading "▸ " chevron and one cell of right padding. Very long
	// custom-action labels will still clip but won't break layout.
	//
	// It is neither a floor nor a ceiling: menuNaturalWidth grows past it
	// for a longer row, and menuModalRect shrinks below it on a terminal
	// too narrow to hold it (down to menuMinFrameWidth), because a frame
	// wider than the screen loses its right border and shortcut column
	// entirely. Height is computed dynamically from the visible groups —
	// see menuLayout.
	modalWidth = 48

	// maxLabelSuffix is the rune budget that newFileLabel /
	// renameFolderLabel / deleteFolderLabel use when truncating their
	// "(in subdir/)" / "(subdir/)" suffix. Pinned alongside modalWidth
	// so the two stay in lockstep — bumping the modal without bumping
	// the suffix budget leaves dead space, and shrinking the modal
	// without shrinking the suffix budget reintroduces the overflow
	// bug where folder names bled into the editor underneath.
	maxLabelSuffix = 30

	// autoScrollTick is how often the auto-scroll goroutine emits a tick
	// while the user is drag-selecting with the cursor parked outside the
	// editor's vertical edges. ~16 ticks/sec feels responsive without
	// overshooting on small files.
	autoScrollTick = 60 * time.Millisecond
)

// dragKind names which surface owns the current mouse drag. Typed so a
// misspelled mode is a compile error instead of a silently dead drag.
type dragKind uint8

const (
	dragNone dragKind = iota
	dragEditor
	dragSidebar
	dragScrollbar
	dragTreeScrollbar
	dragGitPanelScrollbar
)

// App is the editor's top-level state holder and event-loop owner.
type App struct {
	screen tcell.Screen
	theme  theme.Theme
	// themeID is the registry id of the active theme — what the picker
	// pre-selects and what SetTheme persists. See themepick.go.
	themeID string
	// screenColors is what the terminal reported via Screen.Colors()
	// the last time we resolved the palette. Below
	// theme.MinTrueColorPalette the live theme is a theme.Degrade of
	// the authored one — see applyColorDepth.
	screenColors int

	// diskConflicts remembers, per tab path, the on-disk mtime the
	// user was warned about when a dirty buffer diverged from the
	// file. Presence means "unresolved conflict": the status bar keeps
	// its marker up, and reconcileOpenTabsWithDisk will not re-open
	// the conflict overlay for that same disk revision. See
	// conflict.go.
	diskConflicts map[string]time.Time

	// overlays is the single routing truth for which floating modal is
	// up: handleKey, handleMouse, draw, and anyModalOpen all consult it
	// instead of the per-modal booleans. The booleans below survive as
	// modal-internal state until each modal becomes a real overlay
	// adapter. Strips (find, project find, leader) never appear here —
	// see docs/adr/0001-strips-are-not-overlays.md.
	overlays overlay.Stack

	rootDir string
	tree    *filetree.Tree
	// tabs owns the open tabs, the active tab, and the preview-slot
	// rules — its interface speaks *editor.Tab identity, so references
	// held across mutations (modal callbacks, async results) can never
	// act on the wrong tab.
	tabs editor.TabList

	// previewCoachShown records that this session already explained
	// what a preview tab is. Preview tabs replace each other in place,
	// which reads as "my tabs keep vanishing" until someone says the
	// word "preview" — so the first one created flashes an
	// explanation. Once per session, not once per preview: the rule is
	// learned immediately and the flash would otherwise fire on every
	// tree click. See notePreviewCreated.
	previewCoachShown bool

	// activeFolder is the directory the editor is currently "working
	// in" — the default target for New File from the main menu. It
	// updates whenever the user clicks a folder in the tree, opens a
	// file (parent dir wins), or right-clicks a folder. See
	// setActiveFolder for the single write path so the file tree's
	// matching highlight stays in sync.
	activeFolder string

	width, height int

	// sidebarShown controls whether the file explorer panel is visible.
	// When false the editor and tab bar fill the whole window.
	sidebarShown bool

	// sidebarAutoHidden records that applyResponsiveSidebar — not the
	// user — is what hid the explorer, so widening the terminal may put
	// it back. An explicit toggle (the ≡ row or Esc t) clears it, which
	// is the whole point of tracking it apart from sidebarShown: the
	// automatic behavior must never reopen a panel the user
	// deliberately closed.
	sidebarAutoHidden bool

	// sidebarNarrow is the last width verdict applyResponsiveSidebar
	// acted on, which makes the rule edge-triggered: only a crossing of
	// autoHideSidebarWidth does anything. A user who reopens the
	// explorer while the terminal is still narrow therefore keeps it —
	// every later resize inside the same narrow episode is a no-op
	// rather than a fight.
	sidebarNarrow bool

	// wrapOn is the soft-wrap preference stamped onto every tab at
	// creation and flipped (for all open tabs at once) by the menu
	// toggle. Loaded from config.json; defaults to on.
	wrapOn bool

	// sidebarWidth is the live width of the file-explorer block (file tree
	// + 1-cell splitter on its right edge), in screen cells. The user can
	// drag the splitter to change it within [minSidebarWidth, width-minEditorAfterDrag].
	sidebarWidth int

	clipBuf      string
	statusMsg    string
	statusUntil  time.Time
	dragMode     dragKind // dragEditor while a drag-select is active, etc.
	lastClick    clickRecord
	lastTabRects []tabRect

	// lastShiftAt is the wall-clock time we last saw any mouse event
	// carrying the Shift modifier. Some terminals (notably Zellij over
	// macOS Terminal) report modifier state in a separate ButtonNone
	// event right before the wheel event, instead of folding the
	// modifier into the wheel event itself. We treat a wheel event as
	// shifted when one of those modifier-state events arrived within
	// modifierStickyWindow. See handleMouse.
	//
	// That split "modifier state" report is a button-less motion event,
	// so it only arrives under all-motion tracking — which skiff no
	// longer leaves on (see mousemode.go). On those terminals shift +
	// wheel therefore degrades to a plain vertical scroll unless a
	// hover surface happens to be up. Accepted: the wheel event itself
	// is unaffected, terminals that fold the modifier into the wheel
	// report (the common case) are unaffected, and the alternative is
	// flooding every SSH session to rescue one gesture on one stack.
	lastShiftAt time.Time

	// mouseFlags is the mouse-reporting mode the terminal is currently
	// in — the cache that keeps syncMouseMode from re-emitting the same
	// escape run on every overlay open and close. See mousemode.go.
	mouseFlags tcell.MouseFlags

	menuOpen       bool
	hoveredMenuRow int // index into menuItems of the row under the mouse, or -1.
	// menuFilter is the action menu's type-to-filter input, focused
	// from the moment the menu opens. While it holds text menuLayout
	// collapses every group into one flat list of matching rows, so a
	// 40-action menu becomes a command palette instead of a scroll
	// hunt. Reset by openMenu and closeMenu — it never outlives a
	// single showing of the menu.
	menuFilter overlay.Field
	// menuScroll is how many rows the menu's content region is scrolled
	// when the natural layout is taller than the terminal (tmux splits,
	// the 80×24 minimum). 0 whenever everything fits.
	menuScroll int
	lastEscape time.Time // timestamp of the previous Esc press, for double-tap detection.

	// pasting is true between bracketed-paste markers. While it's set,
	// every key event is verbatim content from the terminal's paste
	// buffer — never a command — so handleKey strips raw ESC bytes and
	// suppresses the Esc leader instead of letting pasted text quit the
	// editor or fire menu actions.
	pasting bool

	// Session trash: deleted files/folders are moved here instead of
	// destroyed, so ≡ → Undo delete can bring them back. trashDir is
	// created lazily on first delete; trashed is the restore stack.
	// emptyTrash discards everything when the session ends.
	trashDir string
	trashed  []trashEntry

	// closedTabs is the reopen stack — newest record last. See reopen.go.
	closedTabs []closedTabRecord

	// File clipboard (cut / copy / paste of tree entries) — see fileclip.go.
	fileClipPath string // absolute path on the clipboard; "" = empty
	fileClipCut  bool   // true = paste moves; false = paste copies
	fileOpBusy   bool   // a background move/copy is running

	// tabScroll is how many cells the tab strip is scrolled left when
	// the open tabs are wider than the bar (narrow tmux panes). It is
	// adjusted only at activation sites (ensureActiveTabVisible) and by
	// explicit chevron/wheel scrolling — never in the draw path, for
	// the same reason tab.go's cursorMoved flag exists.
	tabScroll int

	// The prompt modal (single-line text input with OK / Cancel, used by
	// Rename and New File) is an overlay.Prompt prefab — openPrompt in
	// modals.go constructs it; it carries all its own state.

	// The confirm (Yes/No + info flavour) and dirty-close overlays are
	// overlay.Confirm / overlay.Info / overlay.Dirty prefabs — see
	// modals.go for the openers; they carry all their own state.

	// The form and the right-click popup are overlay.Form /
	// overlay.Popup prefabs — see formmodal.go and modals.go for the
	// openers; they carry all their own state.

	// Find bar — opened with Esc-f or the "Find in file" menu entry. The
	// bar is a 1-row strip pinned above the status bar; while it's open
	// it owns the keyboard. The active tab carries the query and match
	// list (see editor.Tab.SetFindQuery), so each tab remembers its own
	// search across closes / reopens.
	findOpen   bool
	findValue  []rune
	findCursor int
	findScroll int
	// Replace field riding the find bar (Tab opens it) — see find.go.
	// replaceScroll is the field's horizontal scroll window, kept
	// caret-tracking by drawFindBar the same way findScroll is.
	replaceOpen      bool
	replaceValue     []rune
	replaceCursor    int
	replaceScroll    int
	findFocusReplace bool

	// The list picker is an overlay.Pick prefab — see listpick.go for
	// the opener; it carries all its own state.

	// Project-wide content search (see projfind.go).
	projFindOpen      bool
	projFindValue     []rune
	projFindCursor    int
	projFindScroll    int
	projFindGen       int // generation counter; stale sweeps are dropped
	projFindBusy      bool
	projFindMatches   []search.Match
	projFindTruncated bool
	projFindSelected  int
	projFindScrollY   int
	projFindFolded    map[string]bool
	projFindLiveGen   atomic.Int64 // latest gen, readable from sweep goroutines
	projFindMatchCase bool
	projFindWholeWord bool
	projFindRegex     bool

	// Project-wide replace riding the panel (see projreplace.go). The
	// X ranges are stamped by drawProjFindBar for the mouse handler.
	projReplaceOpen                        bool
	projReplaceValue                       []rune
	projReplaceCursor                      int
	projFocusReplace                       bool
	projReplaceFieldX0, projReplaceFieldX1 int
	projReplaceAllX0, projReplaceAllX1     int

	// Auto-scroll while drag-selecting past the editor's top/bottom edge.
	// lastDragX/Y is the most recent mouse position so the auto-scroll
	// tick can extend the selection at the user's column even though the
	// mouse hasn't moved.
	autoScrollStop chan struct{}
	autoScrollDir  int // -1 up, 0 idle, +1 down
	lastDragX      int
	lastDragY      int

	// treeRefreshStop signals the background tree-refresh goroutine to exit.
	treeRefreshStop chan struct{}

	// treeScanInFlight marks a background disk sweep for the 10s tick
	// currently running; treeScanQueued remembers that another tick (or
	// a refreshTreeNow call) fired meanwhile, so exactly one follow-up
	// sweep runs when the in-flight one lands. treeScanGen is bumped by
	// every main-thread tree mutation, so a sweep that started before a
	// create/rename/delete is discarded instead of resurrecting the file
	// the user just removed. All three are main-thread-only state.
	treeScanInFlight bool
	treeScanQueued   bool
	treeScanGen      int

	// diffLoadGen is bumped by every async diff request (a gutter-marker
	// click, ≡ → Diff this file). A finished load carries the generation
	// it started at and is dropped unless it still matches, so a diff
	// the user has already clicked past never yanks itself on screen.
	// Main-thread-only.
	diffLoadGen int

	// gitRunner overrides the git process boundary for the async read
	// paths — nil in production (real git), a *git.Fake in tests that
	// need to script a diff without a repo or a subprocess. See
	// App.readRepo.
	gitRunner git.Runner

	// gitSnap is the last applied repo snapshot — branch, ahead/behind,
	// and (via the tree) the changed set. gitSnap.IsRepo is the explicit
	// repo test; nothing infers repo-ness from a non-empty branch name.
	gitSnap git.Snapshot

	// gitRefreshInFlight marks a background status collection currently
	// running; gitRefreshQueued remembers that more refresh requests
	// arrived meanwhile, so exactly one follow-up run fires when the
	// in-flight one lands. Both are main-thread-only state.
	gitRefreshInFlight bool
	gitRefreshQueued   bool

	// gitMissingSeen records that we have already told the user this
	// machine has no git binary. The fact never changes within a
	// process, so the flash is once per session — the 10s status tick
	// would otherwise reprint it forever.
	gitMissingSeen bool

	// customActions is the list of user-configured shell-out actions
	// loaded from ~/.config/skiff/actions.json at startup. When
	// non-empty they prepend a new group to the action menu — see
	// menuLayout. nil / empty when the user hasn't configured any.
	customActions []customactions.Action

	// finder + finder modal state — project-wide file search ("Esc p"
	// or ≡ → Find file). The Finder owns the cached index and a
	// background-build goroutine; the rest of these fields are
	// transient UI state for the modal itself.
	// finder is the long-lived fuzzy-file index cache; the finder UI
	// itself is a bespoke overlay (finder.go's finderOverlay) that owns
	// its transient state.
	finder *finder.Finder

	// Git panel state — the sidebar's second tab ("Esc g", ≡ → Git
	// changes, the GIT header tab, or a click on the status bar's
	// branch segment). When active the sidebar shows the uncommitted-
	// changes list instead of the file tree. Rows are rebuilt from
	// tree.DirtyFiles on activation and on every git-status refresh
	// while the panel is up.
	gitPanelActive bool
	gitPanelRows   []gitChangeRow
	gitPanelScroll int

	// gitDirtyDirs flags which dirty paths are directories, keyed by
	// tree-cased absolute path. Stat'd off-thread by collectGitStatus
	// and rebased alongside DirtyFiles in applyGitStatus, so panel
	// rebuilds between collections (activation, key nav) read the last
	// collection's answer instead of stat'ing on the event loop.
	gitDirtyDirs map[string]bool

	// Git panel keyboard mode (gitchanges.go). The panel is mouse-first,
	// but Button3 and mouse reporting are exactly what macOS Terminal +
	// tmux swallow, so Esc-g / ≡ → Git changes arm a keyboard focus that
	// walks the rows and the action row. All three fields are zero-safe:
	// off, focus on the list, first button.
	gitPanelKeys   bool
	gitPanelOnBtns bool
	gitPanelBtn    int

	// Write-side git state (see gitops.go / gitchanges.go): the
	// one-at-a-time mutation gate, the commit checkbox set (absent =
	// checked), the panel's keyboard/walk selection, and the panel row
	// the open diff came from (-1 = diff not from the panel).
	gitOpBusy         bool
	gitCommitChecks   map[string]bool
	gitPanelSelected  int
	diffPanelRow      int
	gitDeleteTarget   string // branch mid-delete, for the force-delete offer
	gitWorktreeTarget string // worktree mid-remove, for the force-remove offer
	diffBase          string // compare-against ref; "" = HEAD (the default)

	// The diff viewer is a bespoke overlay (diffview.go's diffOverlay)
	// that owns its transient state. diffBase and diffPanelRow stay here:
	// the compare base outlives any one diff, and the panel row marker
	// belongs to the Git panel.

	// The commit-history list is a bespoke overlay (gitlog.go's
	// gitLogOverlay) that owns its transient state.

	quit bool
}

// New initialises the screen and mouse, builds the file tree at rootDir,
// and returns an App ready to Run. rootDir is canonicalized to an
// absolute path immediately: every internal path (tree nodes, tabs,
// activeFolder) is absolute, and keeping the CLI's verbatim "." here
// is what once let the project root pass the "is this a subfolder?"
// guards and become deletable.
func New(rootDir string) (*App, error) {
	if abs, err := filepath.Abs(rootDir); err == nil {
		rootDir = abs
	}
	scr, err := tcell.NewScreen()
	if err != nil {
		return nil, err
	}
	if err := scr.Init(); err != nil {
		return nil, err
	}
	// Baseline mouse reporting only — clicks, drags and the wheel. The
	// all-motion mode (`?1003h`) is switched on per-overlay by
	// syncMouseMode; see mousemode.go for why it is not the default.
	scr.EnableMouse(mouseBaseFlags)
	// Bracketed paste: without it, pasted text arrives as raw
	// keystrokes and any ESC byte inside the paste arms the leader —
	// pasting "\x1bq" would quit the editor. See handleKey's pasting
	// guard.
	scr.EnablePaste()

	th := theme.Default()
	scr.SetStyle(tcell.StyleDefault.Background(th.BG).Foreground(th.Text))
	scr.Clear()

	tree, err := filetree.New(rootDir)
	if err != nil {
		scr.Fini()
		return nil, err
	}

	a := &App{
		screen:         scr,
		mouseFlags:     mouseBaseFlags,
		theme:          th,
		rootDir:        rootDir,
		tree:           tree,
		hoveredMenuRow: -1,
		diffPanelRow:   -1,
		sidebarShown:   true,
		sidebarWidth:   defaultSidebarWidth,
		wrapOn:         true,
	}
	a.setActiveFolder(tree.Root.Path)
	a.loadUserConfig()
	// The config may have swapped in a different palette, so the
	// colour-depth fallback is resolved last — it must degrade the
	// theme the user will actually see, not the default it replaced.
	a.applyColorDepth()
	a.refreshGitStatus()
	a.loadCustomActions()
	a.flash("Welcome — click a file to open · click  ≡  for the menu")
	a.startTreeRefresh()
	// Kick off the project file index in the background so that by
	// the time the user hits Esc-p (or ≡ → Find file) the modal can
	// open with results already in hand. On a 50k-file repo this
	// takes ~150ms; the user pays it once at startup instead of
	// when they're trying to navigate.
	a.finder = finder.New(rootDir)
	// Route the index build through the crash guard: internal/finder
	// can't import internal/app, so the guard rides in as a hook.
	a.finder.PanicGuard = a.safeGo
	scr2 := a.screen
	a.finder.Rebuild(func() {
		_ = scr2.PostEvent(&finderRebuiltEvent{when: time.Now()})
	})
	// Put the user back where they left this project: tabs, cursors,
	// expanded folders, sidebar. Best-effort — no session, no change.
	a.restoreSession()
	return a, nil
}

// NewSingleFile is the lean alternative to New for the "skiff
// somefile.md" invocation: no file tree, no project finder index,
// no background tree-refresh goroutine, sidebar hidden. The user
// asked for one file — we don't pay the cost of walking and watching
// the surrounding directory tree just to render a file they wanted
// to look at in isolation. The tree-toggle row in the action menu
// is filtered out via the hasTree visibility predicate so the user
// can't accidentally try to show a sidebar that doesn't exist.
//
// rootDir is still recorded (set to the file's parent) so file-level
// actions that need a base directory — Save As, New File, the
// relative/absolute path helpers — have somewhere to anchor.
func NewSingleFile(filePath string) (*App, error) {
	scr, err := tcell.NewScreen()
	if err != nil {
		return nil, err
	}
	if err := scr.Init(); err != nil {
		return nil, err
	}
	// Same baseline-mouse rationale as New: no all-motion tracking
	// until an overlay that hovers is actually up.
	scr.EnableMouse(mouseBaseFlags)
	// Same bracketed-paste rationale as New: pasted ESC bytes must be
	// content, not commands.
	scr.EnablePaste()

	th := theme.Default()
	scr.SetStyle(tcell.StyleDefault.Background(th.BG).Foreground(th.Text))
	scr.Clear()

	rootDir := filepath.Dir(filePath)
	if rootDir == "" {
		rootDir = "."
	}
	// Same canonicalization rationale as New: absolute from the start.
	if abs, err := filepath.Abs(rootDir); err == nil {
		rootDir = abs
	}

	a := &App{
		screen:         scr,
		mouseFlags:     mouseBaseFlags,
		theme:          th,
		rootDir:        rootDir,
		tree:           nil,
		hoveredMenuRow: -1,
		diffPanelRow:   -1,
		sidebarShown:   false,
		sidebarWidth:   defaultSidebarWidth,
		wrapOn:         true,
	}
	a.setActiveFolder(rootDir)
	a.loadUserConfig()
	// Same rationale as New: degrade the resolved palette, not the
	// default one.
	a.applyColorDepth()
	a.loadCustomActions()
	// openFile loads the file's git gutter markers itself (a file-scoped
	// `git diff`), so single-file mode shows change bars on open without
	// the whole-repo status or tree walk that New performs.
	a.openFile(filePath)
	return a, nil
}

// applyColorDepth re-derives the live palette for whatever the terminal
// says it can render. Every skiff palette is authored in 24-bit RGB;
// on a 16-colour TERM tcell rounds those onto eight hues and the five
// grays Tokyo Night uses to separate the status bar, the sidebar, and
// the selection all land on the same cell. theme.Degrade answers that
// by spending attributes (reverse, bold, underline) instead of hue.
//
// It is idempotent — Degrade of an already-degraded palette is the same
// palette — so it is safe to call from both constructors and from
// applyTheme when the user picks a new theme mid-session.
func (a *App) applyColorDepth() {
	if a.screen == nil {
		return
	}
	a.screenColors = a.screen.Colors()
	th := theme.Degrade(a.theme, a.screenColors)
	if th == a.theme {
		return // truecolor terminal: the overwhelmingly common case.
	}
	a.theme = th
	a.screen.SetStyle(tcell.StyleDefault.Background(th.BG).Foreground(th.Text))
	// Highlight styles bake the palette in, so every open tab has to
	// re-tokenise against the degraded one.
	for _, t := range a.tabs.Tabs() {
		t.StyleStale = true
	}
}

// Close releases the terminal back to the user. Always call this before
// exit. Screen.Fini drops mouse reporting on its way out — its finalize
// path calls enableMouse(0), which emits
// `\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l` — so whichever mode
// syncMouseMode last put us in, the terminal is left with no tracking
// at all. Nothing here needs to unwind the mode by hand.
func (a *App) Close() {
	a.saveSession()
	// The undo window for deletes is the session: discard the trash on
	// the way out so removed work doesn't pile up invisibly on disk.
	// Living here rather than at the tail of Run means a signal quit or
	// a recovered panic empties it too — every exit path funnels
	// through Close.
	a.emptyTrash()
	a.stopTreeRefresh()
	a.stopAutoScroll()
	if a.screen != nil {
		a.screen.Fini()
	}
}

// Run is the editor's main event loop. It blocks on PollEvent, dispatches
// each event, redraws, and exits when a.quit is set.
func (a *App) Run() error {
	// A panic on the event loop must not reach the runtime with the
	// terminal still raw: restore it, leave a crash log the user can
	// paste into a bug report, and re-panic so the exit code and trace
	// still signal failure. Registered first so it also covers the
	// other defers.
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		a.Close()
		reportCrash("event-loop", handleGoroutinePanic("event-loop", r))
		panic(r)
	}()

	// Route SIGTERM/SIGHUP/SIGINT through the loop as a clean quit so
	// `tmux kill-pane` and friends restore the terminal instead of
	// leaving raw mode and mouse tracking behind.
	stopSignals := a.watchSignals()
	defer stopSignals()

	a.width, a.height = a.screen.Size()
	// Apply the narrow-terminal rule against the size we booted at, not
	// just against later resizes: starting inside a 55-column tmux pane
	// is the exact case this exists for, and tcell does not synthesise
	// an EventResize for the initial size.
	a.applyResponsiveSidebar()
	// Reconcile the mouse mode against the state we booted into before
	// the first paint; every later change rides handleEvent.
	a.syncMouseMode()
	a.draw()
	a.screen.Show()

	for !a.quit {
		ev := a.screen.PollEvent()
		if ev == nil {
			break
		}
		a.handleEvent(ev)
		// Drain everything already queued before paying for a draw. A
		// wheel flick or mouse sweep — especially over SSH, where events
		// arrive in bursts — queues dozens of events; drawing once per
		// burst instead of once per event is the difference between
		// smooth scrolling and syrup on remote links.
		for !a.quit && a.screen.HasPendingEvent() {
			ev = a.screen.PollEvent()
			if ev == nil {
				a.quit = true
				break
			}
			a.handleEvent(ev)
		}
		a.draw()
		a.screen.Show()
	}
	return nil
}

// handleEvent routes a tcell event to its specific handler, then
// reconciles the terminal's mouse-reporting mode against whatever the
// dispatch left on the overlay stack. Doing it here — the one funnel
// every state change enters through — is what makes the mode
// unstrandable: an opener that forgets to pair a call, closeAllModals
// popping a surface out from under an action, or one overlay replacing
// another all land on the same recomputation. See mousemode.go.
func (a *App) handleEvent(ev tcell.Event) {
	defer a.syncMouseMode()

	switch e := ev.(type) {
	case *tcell.EventResize:
		a.width, a.height = a.screen.Size()
		// The tab strip's viewport just changed width — keep the
		// active tab on screen rather than wherever the old scroll
		// left it.
		a.ensureActiveTabVisible()
		a.applyResponsiveSidebar()
		a.screen.Sync()
	case *tcell.EventKey:
		a.handleKey(e)
	case *tcell.EventPaste:
		// Bracketed-paste markers. The pasted content itself still
		// arrives as individual key events; the flag tells handleKey
		// to treat them as verbatim text. The leader window is
		// dropped so a paste can never complete an armed Esc.
		a.pasting = e.Start()
		a.lastEscape = time.Time{}
	case *tcell.EventMouse:
		a.handleMouse(e)
	case *quitRequestEvent:
		// A signal, not a person: skip the dirty-tab modal and quit.
		// Close()'s session save preserves state, and dirty buffers
		// come back dirty on the next open's session restore.
		a.quit = true
	case *autoScrollEvent:
		a.handleAutoScroll()
	case *treeRefreshEvent:
		a.refreshTreeNow()
	case *customActionDoneEvent:
		a.handleCustomActionDone(e)
	case *formatDoneEvent:
		a.handleFormatDone(e)
	case *finderRebuiltEvent:
		// Refresh the open finder's results so a finished index build
		// replaces "Indexing…" without waiting for a keystroke.
		if fo, ok := a.overlays.Top().(*finderOverlay); ok {
			fo.refreshResults()
		}
	case *gitStatusEvent:
		a.handleGitStatusEvent(e)
	case *treeScanEvent:
		a.handleTreeScan(e)
	case *diffLoadEvent:
		a.handleDiffLoaded(e)
	case *projFindDoneEvent:
		a.handleProjFindDone(e)
	case *projFindKickEvent:
		a.handleProjFindKick(e)
	case *projReplaceDoneEvent:
		a.handleProjReplaceDone(e)
	case *gitOpDoneEvent:
		a.handleGitOpDone(e)
	case *fileOpDoneEvent:
		a.handleFileOpDone(e)
	case *fileOpProgressEvent:
		a.handleFileOpProgress(e)
	case *branchListEvent:
		a.handleBranchList(e)
	case *worktreeListEvent:
		a.handleWorktreeList(e)
	}
}

// applyResponsiveSidebar collapses the file explorer when the terminal
// gets too narrow to hold both panels, and puts it back when the
// terminal grows again. Skiff's habitat is a tmux split on a laptop,
// where crossing autoHideSidebarWidth is a routine event rather than an
// exotic one, and an 18-column tree in front of a sliver of code is a
// worse deal than no tree.
//
// Two properties are load-bearing and easy to lose:
//
// It is EDGE-triggered. Only a crossing of the threshold acts, so
// reopening the explorer with Esc t inside a still-narrow window sticks
// instead of being re-hidden by the next stray resize event.
//
// It restores only what it hid. sidebarAutoHidden is set here and
// cleared by menuToggleSidebar, so a panel the user closed on purpose
// stays closed however wide the terminal gets.
func (a *App) applyResponsiveSidebar() {
	// Single-file mode never built a tree, so there is no panel whose
	// width could be crowding anything.
	if a.tree == nil {
		return
	}
	narrow := a.width < autoHideSidebarWidth
	if narrow == a.sidebarNarrow {
		return
	}
	a.sidebarNarrow = narrow
	if narrow {
		if !a.sidebarShown {
			return
		}
		a.sidebarShown = false
		a.sidebarAutoHidden = true
		a.flash("Narrow window — file explorer hidden (Esc t shows it)")
		return
	}
	if !a.sidebarAutoHidden {
		return
	}
	a.sidebarShown = true
	a.sidebarAutoHidden = false
	a.flash("File explorer restored")
}

// flash sets a transient status message that displays for statusFlashFor
// before the status bar reverts to the active file's info. A message too
// long for the bar moves onto its own strip and takes a row off the
// editor, so its expiry gets a scheduled repaint rather than waiting for
// whatever event happens to arrive next — see scheduleFlashStripExpiry.
func (a *App) flash(msg string) {
	a.statusMsg = msg
	a.statusUntil = time.Now().Add(statusFlashFor)
	a.scheduleFlashStripExpiry()
}
