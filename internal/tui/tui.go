package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"loop-o-matic/internal/config"
	"loop-o-matic/internal/core"
	"loop-o-matic/internal/store"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type ModelState int

const (
	stateList ModelState = iota
	stateNewTask
	statePickRepo
	stateConfirmDelete
)

type model struct {
	loops     []core.Loop
	cursor    int
	repoRuns  []core.RepoRun
	state     ModelState
	textInput textinput.Model
	repoNames []string
	repoCursor int
	width     int
	height    int
	store     *store.Store
	cfg       *config.Config
	err       error
	message   string
}

type loopsLoadedMsg struct {
	loops []core.Loop
	err   error
}

type repoRunsLoadedMsg struct {
	runs []core.RepoRun
	err  error
}

type taskCreatedMsg struct {
	err error
}

type loopUpdatedMsg struct {
	err error
}

type tickMsg struct{}
type animMsg struct{}

const tickInterval = 2 * time.Second
const animInterval = 50 * time.Millisecond

func New(cfg *config.Config, s *store.Store) model {
	ti := textinput.New()
	ti.Placeholder = "describe the task..."
	ti.Focus()
	ti.CharLimit = 512
	ti.Width = 60

	return model{
		state:     stateList,
		textInput: ti,
		store:     s,
		cfg:       cfg,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.loadLoops(), tickCmd(), animTickCmd())
}

func tickCmd() tea.Cmd {
	return tea.Tick(tickInterval, func(time.Time) tea.Msg {
		return tickMsg{}
	})
}

func animTickCmd() tea.Cmd {
	return tea.Tick(animInterval, func(time.Time) tea.Msg {
		return animMsg{}
	})
}

func (m model) loadLoops() tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		loops, err := m.store.ListLoops(ctx)
		return loopsLoadedMsg{loops: loops, err: err}
	}
}

func (m model) loadRepoRuns(loopID int64) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		runs, err := m.store.RepoRuns(ctx, loopID)
		return repoRunsLoadedMsg{runs: runs, err: err}
	}
}

func (m model) createTask(text string, repo string) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		issueKey := fmt.Sprintf("TASK-%s", time.Now().Format("20060102-150405"))
		runDir := filepath.Join(m.cfg.Workspace.Root, issueKey)
		if err := os.MkdirAll(runDir, 0o755); err != nil {
			return taskCreatedMsg{err: err}
		}
		scopeText := repo
		ticket := fmt.Sprintf("Local task %s\n\nRequested work:\n%s\n\nRepo scope:\n%s\n", issueKey, text, scopeText)
		ticketPath := filepath.Join(runDir, "ticket.txt")
		if err := os.WriteFile(ticketPath, []byte(ticket), 0o644); err != nil {
			return taskCreatedMsg{err: err}
		}
		loop := &core.Loop{
			IssueKey:   issueKey,
			Summary:    text,
			Status:     core.StatusCreated,
			RunDir:     runDir,
			TicketPath: ticketPath,
			RepoScope:  repo,
		}
		if err := m.store.CreateLoop(ctx, loop); err != nil {
			return taskCreatedMsg{err: err}
		}
		return taskCreatedMsg{err: nil}
	}
}

func (m model) pauseLoop(loop *core.Loop) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		newStatus := core.StatusPaused
		if loop.Status == core.StatusPaused {
			if loop.LastActiveStatus != "" && loop.LastActiveStatus != core.StatusPaused {
				newStatus = loop.LastActiveStatus
			} else {
				newStatus = core.StatusCreated
			}
		}
		err := m.store.UpdateLoopStatus(ctx, loop.ID, newStatus, "")
		return loopUpdatedMsg{err: err}
	}
}

func (m model) cancelLoop(loop *core.Loop) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		err := m.store.UpdateLoopStatus(ctx, loop.ID, core.StatusCancelled, "Cancelled by user")
		return loopUpdatedMsg{err: err}
	}
}

func (m model) deleteLoop(loop *core.Loop) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		_ = m.store.UpdateLoopStatus(ctx, loop.ID, core.StatusCancelled, "Deleted by user")
		time.Sleep(200 * time.Millisecond)
		err := m.store.DeleteLoop(ctx, loop.ID, loop.IssueKey)
		if err == nil {
			_ = os.RemoveAll(loop.RunDir)
		}
		return loopUpdatedMsg{err: err}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case loopsLoadedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.loops = msg.loops
		if m.cursor >= len(m.loops) {
			m.cursor = max(0, len(m.loops)-1)
		}
		if len(m.loops) > 0 && m.cursor < len(m.loops) {
			return m, m.loadRepoRuns(m.loops[m.cursor].ID)
		}
		m.repoRuns = nil
		return m, nil

	case repoRunsLoadedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.repoRuns = msg.runs
		return m, nil

	case taskCreatedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.message = "task created"
		m.state = stateList
		m.textInput.Reset()
		return m, m.loadLoops()

	case loopUpdatedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		return m, tea.Batch(m.loadLoops(), tickCmd())

	case tickMsg:
		return m, tea.Batch(m.loadLoops(), tickCmd())

	case animMsg:
		return m, animTickCmd()

	case tea.KeyMsg:
		m.err = nil
		m.message = ""

		switch m.state {
		case stateList:
			return m.updateList(msg)
		case stateNewTask:
			return m.updateNewTask(msg)
		case statePickRepo:
			return m.updatePickRepo(msg)
		case stateConfirmDelete:
			return m.updateConfirmDelete(msg)
		}
	}

	return m, nil
}

func (m model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case keyMatches(msg, keys.Quit):
		return m, tea.Quit

	case keyMatches(msg, keys.Up):
		if m.cursor > 0 {
			m.cursor--
			return m, m.loadRepoRuns(m.loops[m.cursor].ID)
		}

	case keyMatches(msg, keys.Down):
		if m.cursor < len(m.loops)-1 {
			m.cursor++
			return m, m.loadRepoRuns(m.loops[m.cursor].ID)
		}

	case keyMatches(msg, keys.NewTask):
		m.state = stateNewTask
		m.textInput.Reset()
		m.textInput.Focus()
		return m, textinput.Blink

	case keyMatches(msg, keys.Refresh):
		return m, m.loadLoops()

	case keyMatches(msg, keys.Pause):
		if len(m.loops) > 0 && m.cursor < len(m.loops) {
			return m, m.pauseLoop(&m.loops[m.cursor])
		}

	case keyMatches(msg, keys.Cancel):
		if len(m.loops) > 0 && m.cursor < len(m.loops) {
			loop := &m.loops[m.cursor]
			if !core.IsTerminalStatus(loop.Status) && loop.Status != core.StatusPaused {
				return m, m.cancelLoop(loop)
			}
		}

	case keyMatches(msg, keys.Delete):
		if len(m.loops) > 0 && m.cursor < len(m.loops) {
			loop := &m.loops[m.cursor]
			if core.IsTerminalStatus(loop.Status) || loop.Status == core.StatusPaused || loop.Status == core.StatusCancelled {
				m.state = stateConfirmDelete
			}
		}
	}

	return m, nil
}

func (m model) updateNewTask(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case keyMatches(msg, keys.Escape):
		m.state = stateList
		m.textInput.Blur()
		return m, nil

	case keyMatches(msg, keys.Enter):
		text := strings.TrimSpace(m.textInput.Value())
		if text == "" {
			return m, nil
		}
		m.repoNames = sortedRepoNames(m.cfg.Repos)
		if len(m.repoNames) == 1 {
			return m, m.createTask(text, m.repoNames[0])
		}
		m.state = statePickRepo
		m.repoCursor = 0
		return m, nil

	default:
		var cmd tea.Cmd
		m.textInput, cmd = m.textInput.Update(msg)
		return m, cmd
	}
}

func (m model) updatePickRepo(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case keyMatches(msg, keys.Escape):
		m.state = stateNewTask
		m.textInput.Focus()
		return m, textinput.Blink

	case keyMatches(msg, keys.Up):
		if m.repoCursor > 0 {
			m.repoCursor--
		}

	case keyMatches(msg, keys.Down):
		if m.repoCursor < len(m.repoNames)-1 {
			m.repoCursor++
		}

	case keyMatches(msg, keys.Enter):
		text := strings.TrimSpace(m.textInput.Value())
		repo := m.repoNames[m.repoCursor]
		return m, m.createTask(text, repo)
	}

	return m, nil
}

func (m model) updateConfirmDelete(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case keyMatches(msg, keys.Escape):
		m.state = stateList

	case keyMatches(msg, keys.Enter):
		if len(m.loops) > 0 && m.cursor < len(m.loops) {
			cmd := m.deleteLoop(&m.loops[m.cursor])
			m.state = stateList
			return m, cmd
		}
		m.state = stateList
	}

	return m, nil
}

func (m model) View() string {
	if m.width == 0 || m.height == 0 {
		return "initializing..."
	}

	listWidth := m.width / 3
	detailWidth := m.width - listWidth

	listPanel := m.renderList(listWidth)
	detailPanel := m.renderDetailPanel(detailWidth)

	main := lipgloss.JoinHorizontal(lipgloss.Top, listPanel, detailPanel)

	footer := m.renderFooter()
	return lipgloss.JoinVertical(lipgloss.Left, main, footer)
}

func (m model) renderList(width int) string {
	var items []string

	header := panelTitleStyle.Render("Loops")
	items = append(items, header, "")

	if len(m.loops) == 0 {
		items = append(items, lipgloss.NewStyle().Foreground(colorMuted).Padding(1, 2).Render("No loops found"))
		items = append(items, "")
		items = append(items, lipgloss.NewStyle().Foreground(colorMuted).Padding(0, 2).Render("Press n to create one"))
	} else {
		for i, loop := range m.loops {
			icon := statusIcon(loop.Status)
			statusStr := statusStyle(loop.Status).Render(loop.Status)
			label := fmt.Sprintf(" %s %-12s  %s", icon, loop.IssueKey, statusStr)

			var line string
			if i == m.cursor {
				line = listItemSelectedStyle.Width(width - 4).Render(label)
			} else {
				line = listItemStyle.Width(width - 4).Render(label)
			}
			items = append(items, line)
		}
	}

	content := strings.Join(items, "\n")

	return lipgloss.NewStyle().
		Width(width - 2).
		Height(m.height - 3).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorBorder).
		Padding(1, 1).
		Render(content)
}

func (m model) renderDetailPanel(width int) string {
	if m.state == statePickRepo {
		return m.renderRepoPicker(width)
	}

	if len(m.loops) == 0 || m.cursor >= len(m.loops) {
		return lipgloss.NewStyle().
			Width(width - 2).
			Height(m.height - 3).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder).
			Padding(1, 2).
			Align(lipgloss.Center, lipgloss.Center).
			Foreground(colorMuted).
			Render("Select a loop")
	}

	loop := &m.loops[m.cursor]
	content := renderDetail(loop, m.repoRuns, width-4)

	return lipgloss.NewStyle().
		Width(width - 2).
		Height(m.height - 3).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorBorder).
		Render(content)
}

func (m model) renderRepoPicker(width int) string {
	var items []string

	title := panelTitleStyle.Render("Pick repository")
	items = append(items, title, "")
	items = append(items, helpStyle.Render("Task:"), m.textInput.Value(), "")

	for i, repo := range m.repoNames {
		var line string
		if i == m.repoCursor {
			line = repoItemSelectedStyle.Width(width - 8).Render(" → "+repo)
		} else {
			line = repoItemStyle.Width(width - 8).Render("   "+repo)
		}
		items = append(items, line)
	}

	content := strings.Join(items, "\n")

	return lipgloss.NewStyle().
		Width(width - 2).
		Height(m.height - 3).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorAccent).
		Padding(1, 2).
		Render(content)
}

func (m model) renderFooter() string {
	var parts []string

	switch m.state {
	case stateList:
		parts = []string{
			"↑↓ navigate",
			"n new task",
			"p pause/resume",
			"x cancel",
			"d delete",
			"r refresh",
			"q quit",
		}
	case stateNewTask:
		return inputPromptStyle.Render(" New task: ") + m.textInput.View() + helpStyle.Render("  enter confirm · esc cancel")
	case statePickRepo:
		parts = []string{"↑↓ pick repo", "enter confirm", "esc back"}
	case stateConfirmDelete:
		return confirmStyle.Render(" Delete this loop? ") + helpStyle.Render("enter confirm · esc cancel")
	}

	footer := helpStyle.Render(strings.Join(parts, "  ·  "))
	return lipgloss.NewStyle().
		Width(m.width).
		Padding(0, 1).
		Render(footer)
}

func sortedRepoNames(repos map[string]config.RepoConfig) []string {
	names := make([]string, 0, len(repos))
	for name := range repos {
		names = append(names, name)
	}
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			if names[i] > names[j] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}
	return names
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
