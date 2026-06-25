package tui

import (
	"fmt"
	"strings"
	"time"

	"loop-o-matic/internal/core"

	"github.com/charmbracelet/lipgloss"
)

func renderDetail(loop *core.Loop, repoRuns []core.RepoRun, width int) string {
	if loop == nil {
		return lipgloss.NewStyle().
			Width(width).
			Height(10).
			Align(lipgloss.Center, lipgloss.Center).
			Foreground(colorMuted).
			Render("No loop selected")
	}

	showDiagram := width >= 44

	leftLines := []string{
		renderDetailField("Issue", loop.IssueKey),
		renderDetailField("Status", formatStatus(loop.Status)),
		renderDetailField("Summary", truncateStr(loop.Summary, width-16)),
	}

	if loop.RepoScope != "" {
		leftLines = append(leftLines, renderDetailField("Repos", loop.RepoScope))
	}

	leftLines = append(leftLines, renderDetailField("Created", loop.CreatedAt.Local().Format(time.RFC3339)))
	leftLines = append(leftLines, renderDetailField("Updated", loop.UpdatedAt.Local().Format(time.RFC3339)))

	if loop.AutoRetryCount > 0 {
		leftLines = append(leftLines, renderDetailField("Retries", fmt.Sprintf("%d", loop.AutoRetryCount)))
	}

	if loop.LastError != "" {
		leftLines = append(leftLines, renderDetailField("Error", loop.LastError))
	}

	if loop.PlanPath != "" {
		leftLines = append(leftLines, renderDetailField("Plan", truncateStr(loop.PlanPath, width-16)))
	}

	if len(repoRuns) > 0 {
		leftLines = append(leftLines, "")
		leftLines = append(leftLines, lipgloss.NewStyle().Bold(true).Foreground(colorAccent).Render("Repos"))
		for _, rr := range repoRuns {
			leftLines = append(leftLines, renderRepoRun(rr, width-8))
		}
	}

	leftContent := strings.Join(leftLines, "\n")

	if !showDiagram {
		return lipgloss.NewStyle().
			Width(width).
			Padding(0, 2).
			Render(leftContent)
	}

	colW := width / 2
	rightContent := renderProgress(loop.Status, loop.LastActiveStatus, colW)

	left := lipgloss.NewStyle().
		Width(colW).
		Padding(0, 2).
		Render(leftContent)

	right := lipgloss.NewStyle().
		Width(width - colW).
		Render(rightContent)

	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

func renderDetailField(label, value string) string {
	return detailLabelStyle.Render(label) + "\n" + detailValueStyle.Render(value)
}

func renderRepoRun(rr core.RepoRun, width int) string {
	icon := "○"
	if rr.Changed {
		icon = "●"
	}

	name := lipgloss.NewStyle().Bold(true).Render(rr.RepoName)
	branch := lipgloss.NewStyle().Foreground(colorMuted).Render(rr.Branch)

	line1 := fmt.Sprintf(" %s %s  %s", icon, name, branch)

	var parts []string
	if rr.PRURL != "" {
		parts = append(parts, osc8Link(rr.PRURL, fmt.Sprintf("PR#%d", rr.PRNumber)))
	}

	if rr.CIState != "" {
		ciColor := colorMuted
		switch rr.CIState {
		case "success":
			ciColor = colorSuccess
		case "failure":
			ciColor = colorError
		case "pending":
			ciColor = colorWarning
		}
		parts = append(parts, lipgloss.NewStyle().Foreground(ciColor).Render(rr.CIState))
	}

	if rr.ReviewDecision != "" {
		revColor := colorMuted
		switch rr.ReviewDecision {
		case "APPROVED":
			revColor = colorSuccess
		case "CHANGES_REQUESTED":
			revColor = colorError
		}
		parts = append(parts, lipgloss.NewStyle().Foreground(revColor).Render(formatReview(rr.ReviewDecision)))
	}

	if len(parts) > 0 {
		return line1 + "\n   " + strings.Join(parts, "  ")
	}

	return line1
}

func formatStatus(status string) string {
	icon := statusIcon(status)
	styled := statusStyle(status).Render(status)
	return fmt.Sprintf("%s %s", icon, styled)
}

func formatReview(decision string) string {
	switch decision {
	case "APPROVED":
		return "approved"
	case "CHANGES_REQUESTED":
		return "changes requested"
	case "REVIEW_REQUIRED":
		return "review required"
	default:
		return decision
	}
}

func truncateStr(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-3]) + "..."
}

func osc8Link(url, text string) string {
	style := lipgloss.NewStyle().Foreground(colorAccent)
	return "\033]8;;" + url + "\033\\" + style.Render(text) + "\033]8;;\033\\"
}
