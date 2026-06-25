package tui

import (
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

type keyMap struct {
	Up         key.Binding
	Down       key.Binding
	Enter      key.Binding
	NewTask    key.Binding
	Pause      key.Binding
	Cancel     key.Binding
	Delete     key.Binding
	OpenPR     key.Binding
	Refresh    key.Binding
	Quit       key.Binding
	Escape     key.Binding
	Backspace  key.Binding
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Quit}
}

func (k keyMap) FullHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Enter, k.NewTask, k.Pause, k.Cancel, k.Delete, k.OpenPR, k.Refresh, k.Quit}
}

var keys = keyMap{
	Up: key.NewBinding(
		key.WithKeys("up", "k"),
		key.WithHelp("↑/k", "move up"),
	),
	Down: key.NewBinding(
		key.WithKeys("down", "j"),
		key.WithHelp("↓/j", "move down"),
	),
	Enter: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "select"),
	),
	NewTask: key.NewBinding(
		key.WithKeys("n"),
		key.WithHelp("n", "new task"),
	),
	Pause: key.NewBinding(
		key.WithKeys("p"),
		key.WithHelp("p", "pause/resume"),
	),
	Cancel: key.NewBinding(
		key.WithKeys("x"),
		key.WithHelp("x", "cancel"),
	),
	Delete: key.NewBinding(
		key.WithKeys("d"),
		key.WithHelp("d", "delete"),
	),
	OpenPR: key.NewBinding(
		key.WithKeys("o"),
		key.WithHelp("o", "open PR"),
	),
	Refresh: key.NewBinding(
		key.WithKeys("r"),
		key.WithHelp("r", "refresh"),
	),
	Quit: key.NewBinding(
		key.WithKeys("q", "ctrl+c"),
		key.WithHelp("q", "quit"),
	),
	Escape: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("esc", "back/cancel"),
	),
	Backspace: key.NewBinding(
		key.WithKeys("backspace"),
		key.WithHelp("⌫", "delete char"),
	),
}

func keyMatches(msg tea.KeyMsg, k key.Binding) bool {
	return key.Matches(msg, k)
}
