package main

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func press(b browser, key rune) browser {
	m, _ := b.Update(tea.KeyPressMsg{Code: key, Text: string(key)})
	return m.(browser)
}

func visibleNames(b browser) []string {
	var names []string
	for _, g := range b.visible {
		names = append(names, g.Name)
	}
	return names
}

func TestBrowserHelp(t *testing.T) {
	b := loadedBrowser()
	b = press(b, '?')
	if !b.showHelp || !strings.Contains(b.View().Content, "filter platform") {
		t.Fatal("? didn't show the shortcuts")
	}
	b = press(b, 'p')
	if b.showHelp || b.filters[0].pos != 0 {
		t.Error("a key while help is open should only close it")
	}
	b = press(b, '?')
	if _, cmd := b.Update(tea.KeyPressMsg{Code: 'q', Text: "q"}); cmd != nil {
		t.Error("q while help is open should close help, not quit")
	}
}

func loadedBrowser() browser {
	var b browser
	for _, msg := range []tea.Msg{
		tea.WindowSizeMsg{Width: 100, Height: 20},
		loadedMsg{games: []Game{{Name: "A", Platform: "PS4"}, {Name: "B", Platform: "PS5"}}},
	} {
		m, _ := b.Update(msg)
		b = m.(browser)
	}
	return b
}

func TestBrowserFilters(t *testing.T) {
	var b browser
	for _, msg := range []tea.Msg{
		tea.WindowSizeMsg{Width: 100, Height: 20},
		loadedMsg{games: []Game{
			{Name: "A", Platform: "PS4", Source: "purchased", Accounts: []string{"kids"}},
			{Name: "B", Platform: "PS5", Source: "ps_plus", Accounts: []string{"kids", "main"}},
			{Name: "C", Platform: "PS5", Source: "purchased", Accounts: []string{"main"}},
		}},
	} {
		m, _ := b.Update(msg)
		b = m.(browser)
	}

	for _, step := range []struct {
		key  rune
		want []string
	}{
		{'p', []string{"A"}},      // platform: PS4
		{'p', []string{"B", "C"}}, // platform: PS5
		{'s', []string{"B"}},      // + source: ps_plus
		{'s', []string{"C"}},      // + source: purchased
		{'a', []string{}},         // + account: kids, nothing matches
		{'p', []string{"A"}},      // platform back to all
	} {
		b = press(b, step.key)
		if got := visibleNames(b); !slices.Equal(got, step.want) {
			t.Fatalf("after %q: visible = %v, want %v", step.key, visibleNames(b), step.want)
		}
	}

	b = press(b, 's') // source back to all
	b = press(b, 'a') // account: main
	if got := visibleNames(b); !slices.Equal(got, []string{"B", "C"}) {
		t.Errorf("account main: visible = %v", got)
	}
	b = press(b, 'a') // account back to all
	if got := visibleNames(b); len(got) != 3 {
		t.Errorf("all filters off: visible = %v", got)
	}
}
