package main

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

func noProgress(string) {}

func testStore(t *testing.T) Store {
	keyring.MockInit()
	return Store{path: filepath.Join(t.TempDir(), "plash-scraper", "accounts.json")}
}

func TestStore(t *testing.T) {
	s := testStore(t)

	accounts, err := s.Accounts()
	if err != nil || accounts != nil {
		t.Fatalf("empty store: %v, %v", accounts, err)
	}

	kids := Account{ID: "2", OnlineID: "kids"}
	s.Add(Account{ID: "1", OnlineID: "Main"}, "tok1")
	s.Add(kids, "tok2")
	renamed := Account{ID: "1", OnlineID: "Zed"}
	s.Add(renamed, "tok3")

	accounts, _ = s.Accounts()
	if !slices.Equal(accounts, []Account{kids, renamed}) {
		t.Errorf("accounts = %v", accounts)
	}
	if tok, _ := s.Token(renamed); tok != "tok3" {
		t.Errorf("token = %q, want replaced token", tok)
	}

	if err := s.Remove("KIDS"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Token(kids); err == nil {
		t.Error("kids token still in keychain")
	}
	accounts, _ = s.Accounts()
	if !slices.Equal(accounts, []Account{renamed}) {
		t.Errorf("accounts after remove = %v", accounts)
	}
	if err := s.Remove("nobody"); err == nil {
		t.Error("removing unknown account should fail")
	}
}

func TestLoadGames(t *testing.T) {
	s := testStore(t)
	c := fakePSN(t)
	cachePath := filepath.Join(t.TempDir(), "games.json")
	fetch := func() ([]Game, error) {
		games, _, err := loadGames(s, c, cachePath, true, noProgress)
		return games, err
	}

	if _, err := fetch(); err == nil || !strings.Contains(err.Error(), "no accounts") {
		t.Fatalf("err = %v", err)
	}

	mainAcct := Account{ID: "1", OnlineID: "main"}
	s.Add(mainAcct, "r1")
	s.Add(Account{ID: "2", OnlineID: "kids"}, "r1")
	games, err := fetch()
	if err != nil {
		t.Fatal(err)
	}
	// Both accounts have the same games: one row each, listing both accounts.
	if len(games) != pageSize+2 {
		t.Fatalf("got %d games", len(games))
	}
	for _, g := range games {
		if !slices.Equal(g.Accounts, []string{"kids", "main"}) {
			t.Fatalf("%s accounts = %v", g.Name, g.Accounts)
		}
	}
	if games[0].Name != "Betamax Racer" || games[len(games)-1].Name != "Plus Game" {
		t.Errorf("not sorted by name: %q ... %q", games[0].Name, games[len(games)-1].Name)
	}
	i := slices.IndexFunc(games, func(g Game) bool { return g.Name == "Game 0" })
	if g := games[i]; g.Players() != "1, online 3" || len(g.Genres) != 2 {
		t.Errorf("details not added: %+v", g)
	}
	if tok, _ := s.Token(mainAcct); tok != "r2" {
		t.Errorf("rotated refresh token not saved: %q", tok)
	}

	// r2 gets no new refresh token, so it must be kept.
	if _, err := fetch(); err != nil {
		t.Fatal(err)
	}
	if tok, _ := s.Token(mainAcct); tok != "r2" {
		t.Errorf("refresh token = %q, want r2 kept", tok)
	}

	found := searchGames(games, "plus GAME")
	if len(found) != 1 || found[0].Name != "Plus Game" {
		t.Errorf("search = %+v", found)
	}

	s.Add(Account{ID: "3", OnlineID: "expired"}, "revoked")
	if _, err := fetch(); err == nil || !strings.Contains(err.Error(), "accounts refresh expired") {
		t.Errorf("err = %v", err)
	}
	if err := checkAccounts(s, c); err == nil || !strings.Contains(err.Error(), "1 account(s)") {
		t.Errorf("check err = %v", err)
	}
}

func TestGameCache(t *testing.T) {
	s := testStore(t)
	c := fakePSN(t)
	cachePath := filepath.Join(t.TempDir(), "games.json")
	s.Add(Account{ID: "1", OnlineID: "main"}, "r1")

	first, fetchedAt, err := loadGames(s, c, cachePath, false, noProgress)
	if err != nil {
		t.Fatal(err)
	}

	// With PlayStation unreachable, only the cache can answer.
	c.GraphQLURL, c.AuthURL = "http://127.0.0.1:1", "http://127.0.0.1:1"
	cached, cachedAt, err := loadGames(s, c, cachePath, false, noProgress)
	if err != nil {
		t.Fatalf("cache not used: %v", err)
	}
	if len(cached) != len(first) || !cachedAt.Equal(fetchedAt) || cached[0].StoreURL != first[0].StoreURL {
		t.Errorf("cached games differ: %d vs %d, %v vs %v", len(cached), len(first), cachedAt, fetchedAt)
	}

	if _, _, err := loadGames(s, c, cachePath, true, noProgress); err == nil {
		t.Error("-refresh used the cache")
	}

	if readCache(cachePath, []string{"1"}, fetchedAt.Add(cacheTTL+time.Minute)) != nil {
		t.Error("expired cache used")
	}

	s.Add(Account{ID: "2", OnlineID: "kids"}, "r1")
	if _, _, err := loadGames(s, c, cachePath, false, noProgress); err == nil {
		t.Error("cache used after accounts changed")
	}
}

func TestParseNPSSO(t *testing.T) {
	for in, want := range map[string]string{
		"abc123":                "abc123",
		"  abc123\n":            "abc123",
		`{"npsso":"abc123"}`:    "abc123",
		` {"npsso": "abc123"} `: "abc123",
	} {
		if got := parseNPSSO(in); got != want {
			t.Errorf("parseNPSSO(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRunCommandUsage(t *testing.T) {
	s := testStore(t)
	for _, args := range [][]string{
		{},
		{"accounts"},
		{"accounts", "add", "extra"},
		{"accounts", "remove"},
		{"games", "list", "extra"},
		{"games", "search"},
		{"games", "nope"},
	} {
		if err := runCommand(s, nil, args); err != errUsage {
			t.Errorf("%v: err = %v, want usage", args, err)
		}
	}
}
