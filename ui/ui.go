// Package ui provides the main UI for the glow application.
package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/glow/v3/utils"
	"github.com/charmbracelet/log"
	"github.com/muesli/gitcha"
)

const (
	statusMessageTimeout = time.Second * 3 // how long to show status messages like "stashed!"
	ellipsis             = "…"
)

var markdownExtensions = []string{
	"*.md", "*.mdown", "*.mkdn", "*.mkd", "*.markdown",
}

// NewProgram returns a new Tea program. The returned cleanup function
// restores the terminal state and must be called after the program exits.
func NewProgram(cfg Config, content string) (*tea.Program, func()) {
	log.Debug(
		"Starting glow",
		"glamour",
		cfg.GlamourEnabled,
	)

	m := newModel(cfg, content).(model)
	// Closing the style manager cancels in-flight reloads and waits for their
	// goroutines to exit when the session ends.
	closeStyles := func() { _ = m.common.styleMgr.Close() }

	if utils.TextSizingEnabled() {
		if tty, state, err := openRawTerminal(); err == nil {
			painter := newRawPainter(os.Stdout, tty, state, cfg.EnableMouse)
			prog := tea.NewProgram(
				paintModel{inner: m, painter: painter},
				tea.WithoutRenderer(),
				tea.WithInput(tty),
			)
			painter.watchResize(prog)
			return prog, func() {
				painter.stop()
				closeStyles()
			}
		}
	}

	return tea.NewProgram(m), closeStyles
}

type errMsg struct{ err error }

func (e errMsg) Error() string { return e.err.Error() }

// stylesCommittedMsg delivers a successfully built style snapshot to the
// update loop. The cached styles are swapped once while handling this
// message, which is the single render boundary at which a hot reload takes
// effect.
type stylesCommittedMsg struct {
	snap *StyleSnapshot
}

// stylesReloadFailedMsg reports a background style transaction that failed
// validation. The active snapshot stays in place.
type stylesReloadFailedMsg struct {
	err error
}

// StyleReloadRequestMsg requests a style hot reload from JSON configuration
// data. Send it to the tea program to start a background transaction: the new
// configuration is parsed and fully validated off the update loop and takes
// effect at a single render boundary on success, while failure leaves the
// active styles untouched.
type StyleReloadRequestMsg struct {
	Data []byte
}

type (
	initLocalFileSearchMsg struct {
		cwd string
		ch  chan gitcha.SearchResult
	}
)

type (
	foundLocalFileMsg       gitcha.SearchResult
	localFileSearchFinished struct{}
	statusMessageTimeoutMsg applicationContext
)

// applicationContext indicates the area of the application something applies
// to. Occasionally used as an argument to commands and messages.
type applicationContext int

const (
	stashContext applicationContext = iota
	pagerContext
)

// state is the top-level application state.
type state int

const (
	stateShowStash state = iota
	stateShowDocument
)

func (s state) String() string {
	return map[state]string{
		stateShowStash:    "showing file listing",
		stateShowDocument: "showing document",
	}[s]
}

// Common stuff we'll need to access in all models.
type commonModel struct {
	cfg    Config
	cwd    string
	width  int
	height int
	// styles is the render-boundary cache consumed by View calls on the main
	// update goroutine. It is only swapped while handling a style message.
	styles Styles
	// styleMgr owns the versioned snapshots and runs background reload
	// transactions. Goroutines doing render work must read from it via
	// activeStyles so they observe a single immutable version.
	styleMgr *StyleManager
}

// activeStyles returns the styles of the current version snapshot, falling
// back to the cached styles when no manager is wired up. Callers running
// outside the update loop must retain the returned Styles for the whole
// render instead of re-reading them halfway through.
func (c *commonModel) activeStyles() Styles {
	if c.styleMgr != nil {
		return c.styleMgr.Snapshot().Styles
	}
	return c.styles
}

type model struct {
	common   *commonModel
	state    state
	fatalErr error

	// Sub-models
	stash stashModel
	pager pagerModel

	// Channel that receives paths to local markdown files
	// (via the github.com/muesli/gitcha package)
	localFileFinder chan gitcha.SearchResult
}

// unloadDocument unloads a document from the pager. Note that while this
// method alters the model we also need to send along any commands returned.
func (m *model) unloadDocument() []tea.Cmd {
	m.state = stateShowStash
	m.stash.viewState = stashStateReady
	m.pager.unload()
	m.pager.showHelp = false

	var batch []tea.Cmd
	if !m.stash.shouldSpin() {
		batch = append(batch, m.stash.spinner.Tick)
	}
	return batch
}

func newModel(cfg Config, content string) tea.Model {
	styleMgr := NewDefaultStyleManager(true)
	common := commonModel{
		cfg:      cfg,
		styles:   styleMgr.Snapshot().Styles,
		styleMgr: styleMgr,
	}

	m := model{
		common: &common,
		state:  stateShowStash,
		pager:  newPagerModel(&common),
		stash:  newStashModel(&common),
	}

	path := cfg.Path
	if path == "" && content != "" {
		m.state = stateShowDocument
		m.pager.currentDocument = markdown{Body: content}
		return m
	}

	if path == "" {
		path = "."
	}
	info, err := os.Stat(path)
	if err != nil {
		log.Error("unable to stat file", "file", path, "error", err)
		m.fatalErr = err
		return m
	}
	if info.IsDir() {
		m.state = stateShowStash
	} else {
		cwd, _ := os.Getwd()
		m.state = stateShowDocument
		m.pager.currentDocument = markdown{
			localPath: path,
			Note:      stripAbsolutePath(path, cwd),
			Modtime:   info.ModTime(),
		}
		content, err := os.ReadFile(path)
		if err != nil {
			log.Error("unable to read file", "file", path, "error", err)
			m.fatalErr = err
			return m
		}
		m.pager.currentDocument.Body = string(utils.RemoveFrontmatter(content))
	}

	return m
}

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.stash.spinner.Tick, tea.RequestBackgroundColor}

	switch m.state {
	case stateShowStash:
		cmds = append(cmds, findLocalFiles(*m.common))
	case stateShowDocument:
		cmds = append(cmds, renderWithGlamour(m.pager, m.pager.currentDocument.Body))
	}

	return tea.Batch(cmds...)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// If there's been an error, any key exits
	if m.fatalErr != nil {
		if _, ok := msg.(tea.KeyPressMsg); ok {
			return m, tea.Quit
		}
	}

	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.BackgroundColorMsg:
		// Rebuild from the committed palette and swap atomically, keeping
		// the same single-commit path as a style hot reload.
		if m.common.styleMgr == nil {
			m.common.styles = newStyles(msg.IsDark())
		} else {
			outcome := m.common.styleMgr.SetDark(msg.IsDark())
			if outcome.Err != nil {
				log.Error("failed to switch style theme", "error", outcome.Err)
				break
			}
			m.common.styles = m.common.styleMgr.Snapshot().Styles
		}
		m.stash.stylePaginators(m.common.styles)

	case stylesCommittedMsg:
		// Single render boundary: the cached styles are replaced once here,
		// so the next View renders entirely from the new snapshot while all
		// previous frames kept using the old one.
		m.common.styles = msg.snap.Styles
		m.stash.stylePaginators(m.common.styles)

	case stylesReloadFailedMsg:
		// Validation failed: leave the active snapshot and cached styles
		// untouched.
		log.Error("style hot reload failed; keeping previous styles", "error", msg.err)

	case StyleReloadRequestMsg:
		cmds = append(cmds, reloadStylesCmd(m.common, msg.Data))

	case tea.KeyPressMsg:
		switch msg.String() {
		case "esc":
			if m.state == stateShowDocument || m.stash.viewState == stashStateLoadingDocument {
				batch := m.unloadDocument()
				return m, tea.Batch(batch...)
			}
		case "r":
			var cmd tea.Cmd
			if m.state == stateShowStash {
				// pass through all keys if we're editing the filter
				if m.stash.filterState == filtering {
					m.stash, cmd = m.stash.update(msg)
					return m, cmd
				}
				m.stash.markdowns = nil
				return m, m.Init()
			}

		case "q":
			var cmd tea.Cmd

			switch m.state { //nolint:exhaustive
			case stateShowStash:
				// pass through all keys if we're editing the filter
				if m.stash.filterState == filtering {
					m.stash, cmd = m.stash.update(msg)
					return m, cmd
				}
			}

			return m, tea.Quit

		case "left", "h", "delete":
			if m.state == stateShowDocument {
				cmds = append(cmds, m.unloadDocument()...)
				return m, tea.Batch(cmds...)
			}

		case "ctrl+z":
			return m, tea.Suspend

		// Ctrl+C always quits no matter where in the application you are.
		case "ctrl+c":
			return m, tea.Quit
		}

	// Window size is received when starting up and on every resize
	case tea.WindowSizeMsg:
		m.common.width = msg.Width
		m.common.height = msg.Height
		m.stash.setSize(msg.Width, msg.Height)
		m.pager.setSize(msg.Width, msg.Height)

	case initLocalFileSearchMsg:
		m.localFileFinder = msg.ch
		m.common.cwd = msg.cwd
		cmds = append(cmds, findNextLocalFile(m))

	case fetchedMarkdownMsg:
		// We've loaded a markdown file's contents for rendering
		m.pager.currentDocument = *msg
		body := string(utils.RemoveFrontmatter([]byte(msg.Body)))
		cmds = append(cmds, renderWithGlamour(m.pager, body))

	case contentRenderedMsg:
		m.state = stateShowDocument

	case localFileSearchFinished:
		// Always pass these messages to the stash so we can keep it updated
		// about network activity, even if the user isn't currently viewing
		// the stash.
		stashModel, cmd := m.stash.update(msg)
		m.stash = stashModel
		return m, cmd

	case foundLocalFileMsg:
		newMd := localFileToMarkdown(m.common.cwd, gitcha.SearchResult(msg))
		m.stash.addMarkdowns(newMd)
		if m.stash.filterApplied() {
			newMd.buildFilterValue()
		}
		if m.stash.shouldUpdateFilter() {
			cmds = append(cmds, filterMarkdowns(m.stash))
		}
		cmds = append(cmds, findNextLocalFile(m))

	case filteredMarkdownMsg:
		if m.state == stateShowDocument {
			newStashModel, cmd := m.stash.update(msg)
			m.stash = newStashModel
			cmds = append(cmds, cmd)
		}
	}

	// Process children
	switch m.state {
	case stateShowStash:
		newStashModel, cmd := m.stash.update(msg)
		m.stash = newStashModel
		cmds = append(cmds, cmd)

	case stateShowDocument:
		newPagerModel, cmd := m.pager.update(msg)
		m.pager = newPagerModel
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m model) View() tea.View {
	var content string
	switch {
	case m.fatalErr != nil:
		content = errorView(m.common.styles, m.fatalErr, true)
	case m.state == stateShowDocument:
		content = m.pager.View()
	default:
		content = m.stash.view()
	}

	v := tea.NewView(content)
	v.AltScreen = true
	if m.common.cfg.EnableMouse {
		v.MouseMode = tea.MouseModeCellMotion
	}
	return v
}

func errorView(styles Styles, err error, fatal bool) string {
	exitMsg := "press any key to "
	if fatal {
		exitMsg += "exit"
	} else {
		exitMsg += "return"
	}
	s := fmt.Sprintf("%s\n\n%v\n\n%s",
		styles.errorTitleStyle.Render("ERROR"),
		err,
		styles.subtleStyle.Render(exitMsg),
	)
	return "\n" + indent(s, 3)
}

// COMMANDS

func findLocalFiles(m commonModel) tea.Cmd {
	return func() tea.Msg {
		log.Info("findLocalFiles")
		var (
			cwd = m.cfg.Path
			err error
		)

		if cwd == "" {
			cwd, err = os.Getwd()
		} else {
			var info os.FileInfo
			info, err = os.Stat(cwd)
			if err == nil && info.IsDir() {
				cwd, err = filepath.Abs(cwd)
			}
		}

		// Note that this is one error check for both cases above
		if err != nil {
			log.Error("error finding local files", "error", err)
			return errMsg{err}
		}

		log.Debug("local directory is", "cwd", cwd)

		// Switch between FindFiles and FindAllFiles to bypass .gitignore rules
		var ch chan gitcha.SearchResult
		if m.cfg.ShowAllFiles {
			ch, err = gitcha.FindAllFilesExcept(cwd, markdownExtensions, nil)
		} else {
			ch, err = gitcha.FindFilesExcept(cwd, markdownExtensions, ignorePatterns(m))
		}

		if err != nil {
			log.Error("error finding local files", "error", err)
			return errMsg{err}
		}

		return initLocalFileSearchMsg{ch: ch, cwd: cwd}
	}
}

func findNextLocalFile(m model) tea.Cmd {
	return func() tea.Msg {
		res, ok := <-m.localFileFinder

		if ok {
			// Okay now find the next one
			return foundLocalFileMsg(res)
		}
		// We're done
		log.Debug("local file search finished")
		return localFileSearchFinished{}
	}
}

func waitForStatusMessageTimeout(appCtx applicationContext, t *time.Timer) tea.Cmd {
	return func() tea.Msg {
		<-t.C
		return statusMessageTimeoutMsg(appCtx)
	}
}

// reloadStylesCmd starts a background style transaction over the given JSON
// style configuration. Parsing and full validation happen off the update
// loop; on success a stylesCommittedMsg swaps styles at one render boundary,
// on failure a stylesReloadFailedMsg is delivered and the active styles stay
// untouched.
func reloadStylesCmd(common *commonModel, data []byte) tea.Cmd {
	if common.styleMgr == nil {
		return func() tea.Msg {
			return stylesReloadFailedMsg{err: ErrStyleManagerClosed}
		}
	}
	outcomes := common.styleMgr.Reload(data)
	return func() tea.Msg {
		outcome := <-outcomes
		if outcome.Err != nil {
			return stylesReloadFailedMsg{err: outcome.Err}
		}
		return stylesCommittedMsg{snap: common.styleMgr.Snapshot()}
	}
}

// ETC

// Convert a Gitcha result to an internal representation of a markdown
// document. Note that we could be doing things like checking if the file is
// a directory, but we trust that gitcha has already done that.
func localFileToMarkdown(cwd string, res gitcha.SearchResult) *markdown {
	return &markdown{
		localPath: res.Path,
		Note:      stripAbsolutePath(res.Path, cwd),
		Modtime:   res.Info.ModTime(),
	}
}

func stripAbsolutePath(fullPath, cwd string) string {
	fp, _ := filepath.EvalSymlinks(fullPath)
	cp, _ := filepath.EvalSymlinks(cwd)
	return strings.ReplaceAll(fp, cp+string(os.PathSeparator), "")
}

// Lightweight version of reflow's indent function.
func indent(s string, n int) string {
	if n <= 0 || s == "" {
		return s
	}
	l := strings.Split(s, "\n")
	b := strings.Builder{}
	i := strings.Repeat(" ", n)
	for _, v := range l {
		fmt.Fprintf(&b, "%s%s\n", i, v)
	}
	return b.String()
}
