package tui

import "github.com/charmbracelet/lipgloss"

var (
	colorPrimary   = lipgloss.Color("#7C3AED")
	colorSecondary = lipgloss.Color("#6B7280")
	colorSuccess   = lipgloss.Color("#10B981")
	colorWarning   = lipgloss.Color("#F59E0B")
	colorError     = lipgloss.Color("#EF4444")
	colorMuted     = lipgloss.Color("#9CA3AF")
	colorBg        = lipgloss.Color("#1F2937")
	colorBgLight   = lipgloss.Color("#374151")
	colorFg        = lipgloss.Color("#F9FAFB")
	colorFgDim     = lipgloss.Color("#D1D5DB")
	colorBorder    = lipgloss.Color("#4B5563")
	colorAccent    = lipgloss.Color("#8B5CF6")

	appTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimary).
			Padding(0, 1)

	panelTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorFg).
				Padding(0, 1)

	listItemStyle = lipgloss.NewStyle().
			Padding(0, 1).
			Foreground(colorFgDim)

	listItemSelectedStyle = lipgloss.NewStyle().
				Padding(0, 1).
				Foreground(colorFg).
				Background(colorPrimary).
				Bold(true)

	statusStyle = func(status string) lipgloss.Style {
		switch status {
		case "completed":
			return lipgloss.NewStyle().Foreground(colorSuccess).Bold(true)
		case "failed":
			return lipgloss.NewStyle().Foreground(colorError).Bold(true)
		case "cancelled":
			return lipgloss.NewStyle().Foreground(colorMuted)
		case "paused":
			return lipgloss.NewStyle().Foreground(colorWarning)
		case "implementing", "verifying", "creating_prs", "monitoring_ci":
			return lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
		case "blocked":
			return lipgloss.NewStyle().Foreground(colorError)
		default:
			return lipgloss.NewStyle().Foreground(colorFgDim)
		}
	}

	statusIcon = func(status string) string {
		switch status {
		case "completed":
			return "✓"
		case "failed":
			return "✗"
		case "cancelled":
			return "—"
		case "paused":
			return "◉"
		case "implementing", "verifying", "creating_prs", "monitoring_ci":
			return "►"
		case "blocked":
			return "!"
		default:
			return "○"
		}
	}

	detailLabelStyle = lipgloss.NewStyle().
				Foreground(colorMuted).
				Bold(true)

	detailValueStyle = lipgloss.NewStyle().
				Foreground(colorFg)

	errorStyle = lipgloss.NewStyle().
			Foreground(colorError).
			Italic(true)

	borderedPanel = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder)

	helpStyle = lipgloss.NewStyle().
			Foreground(colorMuted).
			Padding(0, 1)

	inputPromptStyle = lipgloss.NewStyle().
				Foreground(colorPrimary).
				Bold(true)

	confirmStyle = lipgloss.NewStyle().
			Foreground(colorWarning).
			Bold(true)

	repoItemStyle = lipgloss.NewStyle().
			Padding(0, 1).
			Foreground(colorFgDim)

	repoItemSelectedStyle = lipgloss.NewStyle().
				Padding(0, 1).
				Foreground(colorFg).
				Background(colorAccent).
				Bold(true)
)
