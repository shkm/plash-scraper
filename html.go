package main

import (
	_ "embed"
	"html/template"
	"io"
	"strings"
	"time"
)

//go:embed games.html.tmpl
var gamesHTML string

var gamesPage = template.Must(template.New("games").Funcs(template.FuncMap{
	"join":       strings.Join,
	"maxPlayers": func(g Game) int { return max(g.LocalPlayers, g.OnlinePlayers) },
}).Parse(gamesHTML))

// writeHTML writes a self-contained page with a searchable, filterable and
// sortable table of the games.
func writeHTML(w io.Writer, games []Game) error {
	return gamesPage.Execute(w, struct {
		Games                                []Game
		Platforms, Sources, Genres, Accounts []string
		Generated                            string
	}{
		Games:     games,
		Platforms: distinct(games, func(g Game) []string { return []string{g.Platform} }),
		Sources:   distinct(games, func(g Game) []string { return []string{g.Source} }),
		Genres:    distinct(games, func(g Game) []string { return g.Genres }),
		Accounts:  distinct(games, func(g Game) []string { return g.Accounts }),
		Generated: time.Now().Format("2 January 2006"),
	})
}
