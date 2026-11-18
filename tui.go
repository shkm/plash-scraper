package main

import (
	"fmt"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type statusMsg string

type loadedMsg struct {
	games     []Game
	fetchedAt time.Time
	err       error
}

type browser struct {
	load      func() tea.Msg
	spinner   spinner.Model
	status    string
	games     []Game
	fetchedAt time.Time
	visible   []Game
	filters   []filter
	loaded    bool
	showHelp  bool
	table     table.Model
	width     int
	height    int
	err       error
}

var shortcuts = [][2]string{
	{"↑/↓ k/j", "move"},
	{"pgup/pgdn", "page"},
	{"home/end", "first/last game"},
	{"enter o", "open in PlayStation Store"},
	{"m", "search on Metacritic"},
	{"p", "filter platform"},
	{"s", "filter source"},
	{"g", "filter genre"},
	{"a", "filter account"},
	{"?", "show/hide shortcuts"},
	{"q esc", "quit"},
}

// filter narrows the table to one value of a field. Pressing its key cycles
// through "all" and each value present in the games.
type filter struct {
	key     string
	label   string
	values  func(Game) []string
	options []string
	pos     int // 0 means all, otherwise options[pos-1]
}

func (f filter) current() string {
	if f.pos == 0 {
		return "all"
	}
	return f.options[f.pos-1]
}

func (f filter) match(g Game) bool {
	return f.pos == 0 || slices.Contains(f.values(g), f.options[f.pos-1])
}

func newFilters(games []Game) []filter {
	filters := []filter{
		{key: "p", label: "platform", values: func(g Game) []string { return []string{g.Platform} }},
		{key: "s", label: "source", values: func(g Game) []string { return []string{g.Source} }},
		{key: "g", label: "genre", values: func(g Game) []string { return g.Genres }},
		{key: "a", label: "account", values: func(g Game) []string { return g.Accounts }},
	}
	for i, f := range filters {
		filters[i].options = distinct(games, f.values)
	}
	return filters
}

var (
	faint  = lipgloss.NewStyle().Faint(true)
	active = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
)

// browseGames shows a spinner while load runs, then a scrollable, filterable
// table of the games. Enter opens the selected game in the PlayStation Store.
func browseGames(load func(progress func(string)) ([]Game, time.Time, error)) error {
	var p *tea.Program
	b := browser{
		spinner: spinner.New(spinner.WithSpinner(spinner.Dot)),
		status:  "Loading",
		load: func() tea.Msg {
			games, fetchedAt, err := load(func(s string) { p.Send(statusMsg(s)) })
			return loadedMsg{games, fetchedAt, err}
		},
	}
	p = tea.NewProgram(b)
	final, err := p.Run()
	if err != nil {
		return err
	}
	b = final.(browser)
	if b.err != nil {
		return b.err
	}
	if b.loaded && len(b.games) == 0 {
		fmt.Println("No games found.")
	}
	return nil
}

func (b browser) Init() tea.Cmd {
	return tea.Batch(b.spinner.Tick, b.load)
}

func (b browser) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		b.width, b.height = msg.Width, msg.Height
		if b.loaded {
			b.layout()
		}
		return b, nil
	case statusMsg:
		b.status = string(msg)
		return b, nil
	case spinner.TickMsg:
		if b.loaded {
			return b, nil
		}
		var cmd tea.Cmd
		b.spinner, cmd = b.spinner.Update(msg)
		return b, cmd
	case loadedMsg:
		b.loaded = true
		b.games, b.fetchedAt, b.err = msg.games, msg.fetchedAt, msg.err
		if b.err != nil || len(b.games) == 0 {
			return b, tea.Quit
		}
		b.filters = newFilters(b.games)
		b.table = table.New(table.WithFocused(true))
		b.applyFilters()
		return b, nil
	case tea.KeyPressMsg:
		key := msg.String()
		if b.showHelp && key != "ctrl+c" {
			b.showHelp = false
			return b, nil
		}
		switch key {
		case "q", "esc", "ctrl+c":
			return b, tea.Quit
		case "?":
			b.showHelp = b.loaded
			return b, nil
		case "enter", "o":
			if b.loaded && len(b.visible) > 0 {
				return b, openURL(b.visible[b.table.Cursor()].StoreURL)
			}
		case "m":
			if b.loaded && len(b.visible) > 0 {
				return b, openURL(b.visible[b.table.Cursor()].MetacriticURL)
			}
		default:
			for i, f := range b.filters {
				if key == f.key {
					b.filters[i].pos = (f.pos + 1) % (len(f.options) + 1)
					b.applyFilters()
					return b, nil
				}
			}
		}
	}
	if !b.loaded {
		return b, nil
	}
	var cmd tea.Cmd
	b.table, cmd = b.table.Update(msg)
	return b, cmd
}

func (b *browser) applyFilters() {
	b.visible = nil
	for _, g := range b.games {
		if !slices.ContainsFunc(b.filters, func(f filter) bool { return !f.match(g) }) {
			b.visible = append(b.visible, g)
		}
	}
	b.table.SetCursor(0)
	b.layout()
}

// layout sizes the columns to the terminal, giving the name what's left.
// Widths come from all games, so they don't jump around when filtering.
func (b *browser) layout() {
	const platformW, sourceW, genreW, ratingW, playersW, reviewsW, cellPadding = 8, 9, 20, 6, 11, 10, 2
	accountsW := len("ACCOUNTS")
	for _, g := range b.games {
		accountsW = max(accountsW, lipgloss.Width(strings.Join(g.Accounts, ", ")))
	}
	accountsW = min(accountsW, 30)
	nameW := max(20, b.width-platformW-sourceW-genreW-ratingW-playersW-accountsW-reviewsW-8*cellPadding)

	link := func(text, url string) string {
		if url == "" {
			return text
		}
		return lipgloss.NewStyle().Hyperlink(url).Render(text)
	}
	rows := make([]table.Row, len(b.visible))
	for i, g := range b.visible {
		rows[i] = table.Row{
			link(g.Name, g.StoreURL),
			g.Platform,
			g.Source,
			strings.Join(g.Genres, ", "),
			g.Stars(),
			g.Players(),
			strings.Join(g.Accounts, ", "),
			link("Metacritic", g.MetacriticURL),
		}
	}
	b.table.SetColumns([]table.Column{
		{Title: "NAME", Width: nameW},
		{Title: "PLATFORM", Width: platformW},
		{Title: "SOURCE", Width: sourceW},
		{Title: "GENRE", Width: genreW},
		{Title: "RATING", Width: ratingW},
		{Title: "PLAYERS", Width: playersW},
		{Title: "ACCOUNTS", Width: accountsW},
		{Title: "REVIEWS", Width: reviewsW},
	})
	b.table.SetRows(rows)
	b.table.SetWidth(b.width)
	b.table.SetHeight(max(3, b.height-3))
}

func (b browser) View() tea.View {
	if !b.loaded {
		return tea.NewView(b.spinner.View() + " " + b.status + "…\n")
	}
	if b.err != nil || len(b.games) == 0 {
		return tea.NewView("")
	}
	if b.showHelp {
		v := tea.NewView(lipgloss.Place(b.width, b.height, lipgloss.Center, lipgloss.Center, helpView()))
		v.AltScreen = true
		return v
	}

	var parts []string
	for _, f := range b.filters {
		value := f.current()
		if f.pos != 0 {
			value = active.Render(value)
		}
		parts = append(parts, fmt.Sprintf("%s %s: %s", faint.Render(f.key), f.label, value))
	}
	position := "no games match"
	if len(b.visible) > 0 {
		position = fmt.Sprintf("%d/%d", b.table.Cursor()+1, len(b.visible))
	}
	filterBar := position + faint.Render(" · ") + strings.Join(parts, faint.Render(" · "))
	help := faint.Render("? shortcuts · q quit · fetched " + ago(b.fetchedAt, time.Now()) + ", -refresh to fetch again")

	v := tea.NewView(b.table.View() + "\n" + filterBar + "\n" + help)
	v.AltScreen = true
	return v
}

func helpView() string {
	keyStyle := lipgloss.NewStyle().Bold(true).Width(12)
	lines := []string{active.Render("Shortcuts"), ""}
	for _, s := range shortcuts {
		lines = append(lines, keyStyle.Render(s[0])+s[1])
	}
	lines = append(lines, "", faint.Render("Press any key to close"))
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(1, 3).
		Render(strings.Join(lines, "\n"))
}

func openURL(url string) tea.Cmd {
	if url == "" {
		return nil
	}
	return func() tea.Msg {
		opener := "xdg-open"
		if runtime.GOOS == "darwin" {
			opener = "open"
		}
		exec.Command(opener, url).Start()
		return nil
	}
}
