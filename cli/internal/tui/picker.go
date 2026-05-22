package tui

import (
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	cursorStyle    = lipgloss.NewStyle().Underline(true)
	checkmarkStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true)
	greyStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
)

var ErrCancelled = errors.New("selection cancelled")

type pickerModel struct {
	title     string
	items     []string
	cursor    int
	chosen    int
	done      bool
	cancelled bool
}

func (model pickerModel) Init() tea.Cmd { return nil }

func (model pickerModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := message.(tea.KeyMsg)
	if !ok {
		return model, nil
	}

	switch key.String() {
	case "ctrl+c", "esc", "q":
		model.cancelled = true
		return model, tea.Quit
	case "up", "k":
		if model.cursor > 0 {
			model.cursor--
		}
	case "down", "j":
		if model.cursor < len(model.items)-1 {
			model.cursor++
		}
	case "enter":
		model.chosen = model.cursor
		model.done = true
		return model, tea.Quit
	}
	return model, nil
}

func (model pickerModel) View() string {
	if model.cancelled {
		return ""
	}
	if model.done {
		return fmt.Sprintf("%s %s\n", checkmarkStyle.Render("✓"), greyStyle.Render(model.items[model.chosen]))
	}

	var builder strings.Builder
	fmt.Fprintln(&builder, greyStyle.Render("↑/↓ navigate · enter select · q cancel"))
	fmt.Fprintln(&builder, model.title)
	for index, item := range model.items {
		prefix := "  "
		text := item
		if index == model.cursor {
			prefix = "▶ "
			text = cursorStyle.Render(item)
		}
		fmt.Fprintf(&builder, "%s%s\n", prefix, text)
	}
	return builder.String()
}

// Select runs an interactive picker and returns the index of the chosen item.
// Returns ErrCancelled if the user aborts with esc / q / ctrl+c.
func Select(title string, items []string) (int, error) {
	if len(items) == 0 {
		return -1, errors.New("no items to select from")
	}
	finalModel, err := tea.NewProgram(pickerModel{title: title, items: items}).Run()
	if err != nil {
		return -1, fmt.Errorf("picker: %w", err)
	}
	result := finalModel.(pickerModel)
	if result.cancelled || !result.done {
		return -1, ErrCancelled
	}
	return result.chosen, nil
}
