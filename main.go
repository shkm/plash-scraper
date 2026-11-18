package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
	"golang.org/x/term"
)

const usage = `Usage: plash-scraper <command>

Commands:
  accounts add                   Add an account
  accounts list                  List accounts
  accounts check                 Check each account can still sign in
  accounts refresh <online-id>   Sign an account in again with a new token
  accounts remove <online-id>    Remove an account
  games list [options]           List games playable without a disc, on all accounts
  games search [options] <text>  Find games whose name contains <text>

Options for games:
  -format table|json|csv|html  (default table)
  -o <file>                    (default stdout)
  -refresh                     fetch from PlayStation, ignoring cached games
                               (cached games are used for up to 24 hours)

In a terminal, the table is interactive. Press ? for shortcuts.
`

const npssoHelp = `To get the NPSSO token for the account:
  1. Open a private browser window and sign in at https://www.playstation.com
  2. In that window, open https://ca.account.sony.com/api/v1/ssocookie
  3. Copy the whole page, or just the npsso value
  4. Paste it below, then close the window without signing out

`

var errUsage = errors.New("usage")

func main() {
	store, err := DefaultStore()
	if err == nil {
		err = runCommand(store, NewClient(), os.Args[1:])
	}
	if errors.Is(err, errUsage) {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "plash-scraper:", err)
		os.Exit(1)
	}
}

func runCommand(store Store, client *Client, args []string) error {
	if len(args) < 2 {
		return errUsage
	}
	cmd, rest := args[0]+" "+args[1], args[2:]
	switch {
	case cmd == "accounts add" && len(rest) == 0:
		return addAccount(store, client)
	case cmd == "accounts list" && len(rest) == 0:
		accounts, err := store.Accounts()
		if err != nil {
			return err
		}
		for _, a := range accounts {
			fmt.Println(a.OnlineID)
		}
		return nil
	case cmd == "accounts check" && len(rest) == 0:
		return checkAccounts(store, client)
	case cmd == "accounts refresh" && len(rest) == 1:
		return refreshAccount(store, client, rest[0])
	case cmd == "accounts remove" && len(rest) == 1:
		return store.Remove(rest[0])
	case cmd == "games list":
		return listGames(store, client, rest, false)
	case cmd == "games search":
		return listGames(store, client, rest, true)
	default:
		return errUsage
	}
}

func addAccount(store Store, client *Client) error {
	account, tokens, err := signIn(client)
	if err != nil {
		return err
	}
	if err := store.Add(account, tokens.Refresh); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Saved account %s.\n", account.OnlineID)
	return nil
}

func refreshAccount(store Store, client *Client, onlineID string) error {
	existing, err := store.Find(onlineID)
	if err != nil {
		return err
	}
	account, tokens, err := signIn(client)
	if err != nil {
		return err
	}
	if account.ID != existing.ID {
		return fmt.Errorf("that token belongs to %s, not %s", account.OnlineID, existing.OnlineID)
	}
	if err := store.Add(account, tokens.Refresh); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Refreshed account %s.\n", account.OnlineID)
	return nil
}

func checkAccounts(store Store, client *Client) error {
	accounts, err := store.Accounts()
	if err != nil {
		return err
	}
	failed := 0
	for _, a := range accounts {
		if _, err := accessToken(store, client, a); err != nil {
			fmt.Printf("%s: %v\n", a.OnlineID, err)
			failed++
		} else {
			fmt.Printf("%s: ok\n", a.OnlineID)
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d account(s) need signing in again with: plash-scraper accounts refresh <online-id>", failed)
	}
	return nil
}

// signIn asks for an NPSSO token and trades it for OAuth tokens.
func signIn(client *Client) (Account, Tokens, error) {
	fmt.Fprint(os.Stderr, npssoHelp)
	fmt.Fprint(os.Stderr, "Paste token (input hidden): ")
	raw, err := readSecret()
	if err != nil {
		return Account{}, Tokens{}, err
	}
	npsso := parseNPSSO(raw)
	if npsso == "" {
		return Account{}, Tokens{}, errors.New("no token given")
	}

	fmt.Fprintln(os.Stderr, "Signing in to PlayStation...")
	tokens, err := client.Login(npsso)
	if err != nil {
		return Account{}, Tokens{}, err
	}
	account, err := client.Me(tokens.Access)
	return account, tokens, err
}

// accessToken signs in with the account's stored refresh token, saving the
// new refresh token if Sony issues one.
func accessToken(store Store, client *Client, a Account) (string, error) {
	refresh, err := store.Token(a)
	if err != nil {
		return "", err
	}
	tokens, err := client.Refresh(refresh)
	if err != nil {
		return "", fmt.Errorf("sign-in no longer valid: %w", err)
	}
	if tokens.Refresh != "" && tokens.Refresh != refresh {
		if err := store.Add(a, tokens.Refresh); err != nil {
			return "", err
		}
	}
	return tokens.Access, nil
}

func readSecret() (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && !(errors.Is(err, io.EOF) && line != "") {
			return "", err
		}
		return line, nil
	}
	b, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	return string(b), err
}

// parseNPSSO accepts either the bare token or the JSON shown on the ssocookie page.
func parseNPSSO(s string) string {
	s = strings.TrimSpace(s)
	var page struct {
		NPSSO string `json:"npsso"`
	}
	if json.Unmarshal([]byte(s), &page) == nil && page.NPSSO != "" {
		return page.NPSSO
	}
	return s
}

func listGames(store Store, client *Client, args []string, search bool) error {
	fs := flag.NewFlagSet("games", flag.ExitOnError)
	format := fs.String("format", "table", "output format: table, json, csv or html")
	out := fs.String("o", "", "output file (default stdout)")
	refresh := fs.Bool("refresh", false, "fetch from PlayStation instead of using cached games")
	fs.Parse(args)

	query := strings.Join(fs.Args(), " ")
	if search != (query != "") {
		return errUsage
	}
	if !slices.Contains(formats, *format) {
		return fmt.Errorf("unknown format %q (want table, json, csv or html)", *format)
	}
	cachePath, err := defaultCachePath()
	if err != nil {
		return err
	}

	load := func(progress func(string)) ([]Game, time.Time, error) {
		games, fetchedAt, err := loadGames(store, client, cachePath, *refresh, progress)
		if search {
			games = searchGames(games, query)
		}
		return games, fetchedAt, err
	}
	if *format == "table" && *out == "" && term.IsTerminal(int(os.Stdout.Fd())) {
		return browseGames(load)
	}

	games, _, err := load(func(string) {})
	if err != nil {
		return err
	}
	if *out == "" {
		return writeGames(os.Stdout, *format, games)
	}
	f, err := os.Create(*out)
	if err != nil {
		return err
	}
	return errors.Join(writeGames(f, *format, games), f.Close())
}

// loadGames returns the cached games if they're fresh and for the same
// accounts, otherwise fetches them from PlayStation and caches them.
func loadGames(store Store, client *Client, cachePath string, refresh bool, progress func(string)) ([]Game, time.Time, error) {
	accounts, err := store.Accounts()
	if err != nil {
		return nil, time.Time{}, err
	}
	if len(accounts) == 0 {
		return nil, time.Time{}, errors.New("no accounts; add one with: plash-scraper accounts add")
	}
	ids := make([]string, len(accounts))
	for i, a := range accounts {
		ids[i] = a.ID
	}

	now := time.Now()
	if !refresh {
		if c := readCache(cachePath, ids, now); c != nil {
			return c.Games, c.FetchedAt, nil
		}
	}
	games, err := allGames(store, client, accounts, progress)
	if err != nil {
		return nil, time.Time{}, err
	}
	// A failed write only means fetching again next time.
	writeCache(cachePath, gameCache{Version: cacheVersion, FetchedAt: now, AccountIDs: ids, Games: games})
	return games, now, nil
}

func allGames(store Store, client *Client, accounts []Account, progress func(string)) ([]Game, error) {
	// One row per game: the same game on several accounts gets one row listing
	// them all. Different sources stay separate rows so source stays accurate.
	type key struct{ titleID, name, source string }
	index := map[key]int{}
	var games []Game
	var token string
	for _, a := range accounts {
		progress(fmt.Sprintf("Signing in as %s", a.OnlineID))
		var err error
		token, err = accessToken(store, client, a)
		if err != nil {
			return nil, fmt.Errorf("account %s: %w; sign in again with: plash-scraper accounts refresh %s", a.OnlineID, err, a.OnlineID)
		}
		progress(fmt.Sprintf("Fetching games for %s", a.OnlineID))
		accountGames, err := client.Games(token)
		if err != nil {
			return nil, fmt.Errorf("account %s: %w", a.OnlineID, err)
		}
		for _, g := range accountGames {
			k := key{g.TitleID, g.Name, g.Source}
			if i, ok := index[k]; ok {
				if !slices.Contains(games[i].Accounts, a.OnlineID) {
					games[i].Accounts = append(games[i].Accounts, a.OnlineID)
				}
				continue
			}
			g.Accounts = []string{a.OnlineID}
			index[k] = len(games)
			games = append(games, g)
		}
	}

	progress("Fetching genres, ratings and player counts")
	addDetails(client, token, games)

	slices.SortStableFunc(games, func(a, b Game) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return games, nil
}

// addDetails fills in genres and player counts, a few requests at a time.
// They're extras, so a game whose details can't be fetched (for example one
// that's left the store) just goes without.
func addDetails(client *Client, token string, games []Game) {
	var g errgroup.Group
	g.SetLimit(8)
	for i := range games {
		if games[i].ConceptID == "" {
			continue
		}
		g.Go(func() error {
			if d, err := client.ConceptDetails(token, games[i].ConceptID); err == nil {
				g := &games[i]
				g.Genres, g.Rating, g.RatingCount = d.Genres, d.Rating, d.RatingCount
				g.LocalPlayers, g.OnlinePlayers = d.LocalPlayers, d.OnlinePlayers
			}
			return nil
		})
	}
	g.Wait()
}

func searchGames(games []Game, query string) []Game {
	query = strings.ToLower(query)
	var found []Game
	for _, g := range games {
		if strings.Contains(strings.ToLower(g.Name), query) {
			found = append(found, g)
		}
	}
	return found
}
