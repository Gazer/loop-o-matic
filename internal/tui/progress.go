package tui

import (
	"regexp"
	"strings"
	"time"

	"loop-o-matic/internal/core"

	"github.com/charmbracelet/lipgloss"
)

var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func visLen(s string) int {
	return len([]rune(ansiRegex.ReplaceAllString(s, "")))
}

func rotatingBox(inner, topBorder, border string, c lipgloss.TerminalColor, center int) (string, string, string) {
	topRunes := []rune("╭" + topBorder + "╮")
	midRunes := []rune("│" + inner + "│")
	botRunes := []rune("╰" + border + "╯")

	type pos struct{ line, col int }
	var borderPos []pos

	for j := 0; j < len(topRunes); j++ {
		borderPos = append(borderPos, pos{0, j})
	}
	borderPos = append(borderPos, pos{1, len(midRunes) - 1})
	for j := len(botRunes) - 1; j >= 0; j-- {
		borderPos = append(borderPos, pos{2, j})
	}
	borderPos = append(borderPos, pos{1, 0})

	totalPerim := len(borderPos)
	curPos := int((time.Now().UnixMilli() / 100) % int64(totalPerim))

	bright := lipgloss.NewStyle().Foreground(lipgloss.Color("#E9D5FF")).Bold(true)
	medium := lipgloss.NewStyle().Foreground(lipgloss.Color("#C4B5FD"))
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("#8B5CF6"))
	def := lipgloss.NewStyle().Foreground(c)

	lines := []string{string(topRunes), string(midRunes), string(botRunes)}
	result := make([]string, 3)

	for lineIdx := 0; lineIdx < 3; lineIdx++ {
		runes := []rune(lines[lineIdx])
		var buf strings.Builder
		for colIdx, r := range runes {
			isBorderChar := r == '╭' || r == '╮' || r == '╯' || r == '╰' || r == '│' || r == '─'
			if !isBorderChar {
				buf.WriteString(def.Render(string(r)))
				continue
			}

			bestDist := totalPerim
			for pi, p := range borderPos {
				if p.line == lineIdx && p.col == colIdx {
					d := (curPos - pi + totalPerim) % totalPerim
					if d < bestDist {
						bestDist = d
					}
				}
			}

			switch {
			case bestDist == 0:
				buf.WriteString(bright.Render(string(r)))
			case bestDist == 1:
				buf.WriteString(medium.Render(string(r)))
			case bestDist == 2:
				buf.WriteString(dim.Render(string(r)))
			default:
				buf.WriteString(def.Render(string(r)))
			}
		}
		result[lineIdx] = buf.String()
	}

	return result[0], result[1], result[2]
}

var flowSteps = []struct {
	status     string
	label      string
	labelShort string
	feedback   bool
}{
	{core.StatusCreated, "created", "crt", false},
	{core.StatusPreparingWorkspace, "preparing", "prep", false},
	{core.StatusDiscovering, "discovering", "disc", false},
	{core.StatusImplementing, "implementing", "impl", false},
	{core.StatusVerifying, "verifying", "ver", true},
	{core.StatusCreatingPRs, "creating_prs", "prs", false},
	{core.StatusMonitoringCI, "monitoring_ci", "ci", true},
	{core.StatusWaitingHumanReview, "review", "rev", true},
	{core.StatusCompleted, "done", "done", false},
}

func renderProgress(currentStatus, lastActiveStatus string, width int) string {
	if width < 14 {
		return ""
	}
	if currentStatus == core.StatusPaused {
		return renderPausedFlow(lastActiveStatus, width)
	}
	if core.IsTerminalStatus(currentStatus) && currentStatus != core.StatusCompleted {
		return renderFailedFlow(lastActiveStatus, currentStatus, width)
	}

	currentIdx := -1
	for i, step := range flowSteps {
		if step.status == currentStatus {
			currentIdx = i
			break
		}
	}
	if currentIdx == -1 {
		return renderTerminalStatus(currentStatus, width)
	}
	return renderFlowDiagram(currentIdx, -1, "", width)
}

func stepColor(idx, focusIdx, failIdx int, failStatus string) lipgloss.TerminalColor {
	if idx < focusIdx && (failIdx == -1 || idx < failIdx) {
		return colorSuccess
	}
	if idx == focusIdx && failIdx == -1 {
		return colorPrimary
	}
	if idx == failIdx {
		switch failStatus {
		case core.StatusFailed:
			return colorError
		case core.StatusCancelled:
			return colorMuted
		case core.StatusBlocked:
			return colorError
		default:
			return colorWarning
		}
	}
	return colorBorder
}

func stepBold(idx, focusIdx, failIdx int, failStatus string) bool {
	return (idx == focusIdx && failIdx == -1) || idx == failIdx
}

func styled(st lipgloss.TerminalColor, bold bool, s string) string {
	style := lipgloss.NewStyle().Foreground(st)
	if bold {
		style = style.Bold(true)
	}
	return style.Render(s)
}

func renderFlowDiagram(focusIdx, failIdx int, failStatus string, width int) string {
	showFb := width >= 26
	maxLabel := 14
	if showFb {
		maxLabel = 16
	}

	fullMax := 0
	shortMax := 0
	for _, step := range flowSteps {
		if len(step.label) > fullMax {
			fullMax = len(step.label)
		}
		if len(step.labelShort) > shortMax {
			shortMax = len(step.labelShort)
		}
	}

	useShort := fullMax+2 > maxLabel
	boxW := shortMax + 4
	if !useShort {
		boxW = fullMax + 4
	}
	if boxW < 8 {
		boxW = 8
	}
	if boxW > maxLabel+2 {
		boxW = maxLabel + 2
	}

	fb := lipgloss.NewStyle().Foreground(colorWarning).Faint(true)
	fbB := lipgloss.NewStyle().Foreground(colorWarning).Bold(true)

	boxTotal := boxW
	border := strings.Repeat("─", boxW-2)
	center := boxTotal / 2

	var lines []string

	for i, step := range flowSteps {
		c := stepColor(i, focusIdx, failIdx, failStatus)
		b := stepBold(i, focusIdx, failIdx, failStatus)

		label := step.label
		if useShort {
			label = step.labelShort
		}
		innerW := boxW - 2
		maxLen := innerW - 2
		if maxLen < 1 {
			maxLen = 1
		}
		if len(label) > maxLen {
			label = label[:maxLen]
		}

		lp := (innerW - len(label) - 2) / 2
		if lp < 0 {
			lp = 0
		}
		rp := innerW - len(label) - 2 - lp
		if rp < 0 {
			rp = 0
		}
		inner := strings.Repeat("─", lp) + " " + label + " " + strings.Repeat("─", rp)

		if i == focusIdx && failIdx == -1 {
			topBorder := border
			if i > 0 {
				topRunes := []rune(border)
				topRunes[center-1] = '▼'
				topBorder = string(topRunes)
			}
			top, mid, bot := rotatingBox(inner, topBorder, border, c, center)
			lines = append(lines, top)
			lines = append(lines, mid)
			lines = append(lines, bot)
		} else {
			top := border
			if i > 0 {
				topRunes := []rune(border)
				topRunes[center-1] = '▼'
				top = string(topRunes)
			}
			lines = append(lines, styled(c, b, "╭"+top+"╮"))
			lines = append(lines, styled(c, b, "│"+inner+"│"))
			lines = append(lines, styled(c, b, "╰"+border+"╯"))
		}

		if i < len(flowSteps)-1 {
			lines = append(lines, styled(c, b, strings.Repeat(" ", center)+"│"))
		}
	}

	fbLines := make([]string, len(lines))

	if showFb {
		fbTarget := 13
		fbVerify := 17
		fbCI := 25
		fbReview := 29

		fbLines[fbTarget] = fbB.Render("◀───────┐")
		for j := fbTarget + 1; j < fbVerify; j++ {
			if fbLines[j] == "" {
				fbLines[j] = fb.Render("        │")
			}
		}
		fbLines[fbVerify] = fb.Render("────────┤")
		for j := fbVerify + 1; j < fbCI; j++ {
			if fbLines[j] == "" {
				fbLines[j] = fb.Render("        │")
			}
		}
		fbLines[fbCI] = fb.Render("────────┤")
		for j := fbCI + 1; j < fbReview; j++ {
			if fbLines[j] == "" {
				fbLines[j] = fb.Render("        │")
			}
		}
		fbLines[fbReview] = fb.Render("────────┘")
	}

	leftWidth := 0
	for _, l := range lines {
		w := visLen(l)
		if w > leftWidth {
			leftWidth = w
		}
	}

	fbWidth := 0
	for _, l := range fbLines {
		w := visLen(l)
		if w > fbWidth {
			fbWidth = w
		}
	}

	centerZone := width
	if showFb {
		centerZone = width - fbWidth - 2
	}
	if centerZone < leftWidth {
		centerZone = leftWidth
	}

	var combined []string
	for i := 0; i < len(lines); i++ {
		w := visLen(lines[i])
		p := leftWidth - w
		padded := lines[i] + strings.Repeat(" ", p)
		leftGap := (centerZone - leftWidth) / 2
		centered := strings.Repeat(" ", leftGap) + padded

		r := fbLines[i]
		if showFb && r != "" {
			combined = append(combined, centered+"  "+r)
		} else {
			combined = append(combined, centered)
		}
	}

	return strings.Join(combined, "\n")
}

func renderFailedFlow(lastActiveStatus, terminalStatus string, width int) string {
	failIdx := -1
	for i, step := range flowSteps {
		if step.status == lastActiveStatus {
			failIdx = i
			break
		}
	}
	if failIdx == -1 {
		return renderTerminalStatus(terminalStatus, width)
	}
	return renderFlowDiagram(failIdx, failIdx, terminalStatus, width)
}

func renderPausedFlow(lastActiveStatus string, width int) string {
	pauseIdx := -1
	for i, step := range flowSteps {
		if step.status == lastActiveStatus {
			pauseIdx = i
			break
		}
	}
	if pauseIdx == -1 {
		return renderTerminalStatus(core.StatusPaused, width)
	}
	return renderFlowDiagram(pauseIdx, -1, "", width)
}

func renderTerminalStatus(status string, width int) string {
	var icon, label string
	var c lipgloss.TerminalColor

	switch status {
	case core.StatusFailed:
		icon = "✗"
		label = "failed"
		c = colorError
	case core.StatusCancelled:
		icon = "—"
		label = "cancelled"
		c = colorMuted
	case core.StatusPaused:
		icon = "⏸"
		label = "paused"
		c = colorWarning
	case core.StatusBlocked:
		icon = "!"
		label = "blocked"
		c = colorError
	default:
		icon = "?"
		label = status
		c = colorMuted
	}

	st := lipgloss.NewStyle().Foreground(c).Bold(true)
	box := st.Render("[" + label + "]")
	gap := width - visLen(icon) - 1 - visLen(box)
	if gap < 0 {
		gap = 0
	}
	leftPad := gap / 2
	return strings.Repeat(" ", leftPad) + icon + " " + box
}
