package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
)

var formats = []string{"table", "json", "csv", "html"}

func writeGames(w io.Writer, format string, games []Game) error {
	switch format {
	case "table":
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tPLATFORM\tSOURCE\tGENRE\tRATING\tPLAYERS\tACCOUNTS")
		for _, g := range games {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", g.Name, g.Platform, g.Source,
				strings.Join(g.Genres, ", "), g.Stars(), g.Players(), strings.Join(g.Accounts, ", "))
		}
		return tw.Flush()
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if games == nil {
			games = []Game{}
		}
		return enc.Encode(games)
	case "csv":
		cw := csv.NewWriter(w)
		cw.Write([]string{"name", "platform", "source", "genres", "rating", "rating_count", "local_players", "online_players",
			"accounts", "title_id", "store_url", "metacritic_url", "image_url"})
		for _, g := range games {
			rating := ""
			if g.RatingCount > 0 {
				rating = strconv.FormatFloat(g.Rating, 'f', 2, 64)
			}
			cw.Write([]string{g.Name, g.Platform, g.Source, strings.Join(g.Genres, ", "), rating, count(g.RatingCount),
				count(g.LocalPlayers), count(g.OnlinePlayers), strings.Join(g.Accounts, ", "),
				g.TitleID, g.StoreURL, g.MetacriticURL, g.ImageURL})
		}
		cw.Flush()
		return cw.Error()
	case "html":
		return writeHTML(w, games)
	default:
		return fmt.Errorf("unknown format %q (want table, json, csv or html)", format)
	}
}

// distinct returns the sorted, unique values of a field across games.
func distinct(games []Game, values func(Game) []string) []string {
	var out []string
	for _, g := range games {
		for _, v := range values(g) {
			if !slices.Contains(out, v) {
				out = append(out, v)
			}
		}
	}
	slices.Sort(out)
	return out
}

// count leaves unknown (zero) counts empty.
func count(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}
