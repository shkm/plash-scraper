package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func fakePSN(t *testing.T) *Client {
	mux := http.NewServeMux()
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		if c, _ := r.Cookie("npsso"); c == nil || c.Value != "good" {
			w.Header().Set("Location", redirectURI+"/?error=login_required")
		} else {
			w.Header().Set("Location", redirectURI+"/?code=v3.abc&cid=x")
		}
		w.WriteHeader(http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != basicAuth {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.FormValue("grant_type") + " " + r.FormValue("code") + r.FormValue("refresh_token") {
		case "authorization_code v3.abc":
			fmt.Fprint(w, `{"access_token":"tok","refresh_token":"r1"}`)
		case "refresh_token r1": // Sony may rotate the refresh token...
			fmt.Fprint(w, `{"access_token":"tok","refresh_token":"r2"}`)
		case "refresh_token r2": // ...or not issue a new one.
			fmt.Fprint(w, `{"access_token":"tok"}`)
		default:
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
		}
	})
	mux.HandleFunc("/graphql", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, `{"errors":[{"message":"blocked as CSRF"}]}`, http.StatusBadRequest)
			return
		}
		var v struct {
			Start      int     `json:"start"`
			Membership *string `json:"membership"`
		}
		json.Unmarshal([]byte(r.URL.Query().Get("variables")), &v)

		var games []map[string]any
		switch {
		case v.Membership != nil:
			// Like Sony: filtering by membership returns nothing.
		case v.Start == 0:
			for i := range pageSize {
				games = append(games, map[string]any{"name": fmt.Sprintf("Game %d", i), "titleId": fmt.Sprintf("T%d", i), "entitlementId": fmt.Sprintf("E%d", i), "membership": "NONE", "image": map[string]any{"url": "http://img"}})
			}
		case v.Start == pageSize:
			games = append(games,
				map[string]any{"name": "Preorder", "titleId": "TPRE", "entitlementId": "P", "membership": "NONE", "isPreOrder": true},
				map[string]any{"name": "Plus Game", "titleId": "TPLUS", "entitlementId": "PLUS1", "membership": "PS_PLUS"},
				map[string]any{"name": "Netflix", "titleId": "TAPP", "entitlementId": "APP1", "membership": "NONE"},
				map[string]any{"name": "Some Game BETA", "titleId": "TBETA", "entitlementId": "BETA1", "membership": "NONE"},
				map[string]any{"name": "Platinum Demo – Big RPG", "titleId": "TDEMO", "entitlementId": "DEMO1", "membership": "NONE"},
				map[string]any{"name": "Betamax Racer", "titleId": "TBM", "entitlementId": "BM1", "membership": "NONE"},
				map[string]any{"name": "The Art of Some Game", "titleId": "TART", "entitlementId": "ART1", "membership": "NONE"},
			)
		}
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"purchasedTitlesRetrieve": map[string]any{"games": games}}})
	})
	mux.HandleFunc("/entitlements", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("fields") != "titleMeta,conceptMeta" {
			http.Error(w, "missing fields", http.StatusBadRequest)
			return
		}
		// The app and art book are on the second page.
		ents := []map[string]any{{"id": "E0", "featureType": 3, "conceptMeta": map[string]any{"conceptId": "100"}}}
		if r.URL.Query().Get("offset") == fmt.Sprint(entitlementPageSize) {
			ents = []map[string]any{
				{"id": "APP1", "featureType": 1},
				{"id": "ART1", "featureType": 3, "titleMeta": map[string]any{"name": "Some Game Digital Art Book"}},
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"totalResults": entitlementPageSize + 1, "entitlements": ents})
	})
	mux.HandleFunc("/profile/me/profile2", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" || r.URL.Query().Get("fields") != "onlineId,accountId" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `{"profile":{"onlineId":"Player1","accountId":"123"}}`)
	})
	mux.HandleFunc("/catalog/100", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `{"localizedGenres":[{"type":"ACTION","value":"Action"},{"type":"RPG","value":"Role Playing Games"},{"type":"ACTION","value":"Action"}],
			"starRating":{"score":"4.32","total":"21547"},
			"defaultProduct":{"compatibilityNotices":[{"type":"NO_OF_PLAYERS","value":1},{"type":"NO_OF_NETWORK_PLAYERS_PS_PLUS","value":3},{"type":"REMOTE_PLAY_SUPPORTED","value":true}]}}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := NewClient()
	c.AuthURL = srv.URL
	c.GraphQLURL = srv.URL + "/graphql"
	c.ProfileURL = srv.URL + "/profile"
	c.EntitlementsURL = srv.URL + "/entitlements"
	c.CatalogURL = srv.URL + "/catalog"
	return c
}

func TestConceptDetails(t *testing.T) {
	c := fakePSN(t)
	d, err := c.ConceptDetails("tok", "100")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(d.Genres, []string{"Action", "Role Playing Games"}) || d.LocalPlayers != 1 || d.OnlinePlayers != 3 ||
		d.Rating != 4.32 || d.RatingCount != 21547 {
		t.Errorf("details = %+v", d)
	}
	if _, err := c.ConceptDetails("tok", "404"); err == nil {
		t.Error("expected error for unknown concept")
	}
}

func TestStars(t *testing.T) {
	if got := (Game{Rating: 4.32, RatingCount: 10}).Stars(); got != "4.3★" {
		t.Errorf("Stars() = %q", got)
	}
	if got := (Game{}).Stars(); got != "" {
		t.Errorf("unrated Stars() = %q", got)
	}
}

func TestPlayers(t *testing.T) {
	for _, tc := range []struct {
		local, online int
		want          string
	}{
		{0, 0, ""},
		{1, 0, "1"},
		{0, 4, "online 4"},
		{2, 4, "2, online 4"},
	} {
		if got := (Game{LocalPlayers: tc.local, OnlinePlayers: tc.online}).Players(); got != tc.want {
			t.Errorf("Players(%d, %d) = %q, want %q", tc.local, tc.online, got, tc.want)
		}
	}
}

func TestMetacriticURL(t *testing.T) {
	got := metacriticURL("Horizon Zero Dawn™: Complete  Edition")
	want := "https://www.metacritic.com/search/Horizon%20Zero%20Dawn%20:%20Complete%20Edition/?category=13"
	if got != want {
		t.Errorf("got %s\nwant %s", got, want)
	}
}

func TestMe(t *testing.T) {
	c := fakePSN(t)
	got, err := c.Me("tok")
	if err != nil {
		t.Fatal(err)
	}
	if want := (Account{ID: "123", OnlineID: "Player1"}); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if _, err := c.Me("bad"); err == nil {
		t.Error("expected error for bad token")
	}
}

func TestLoginAndGames(t *testing.T) {
	c := fakePSN(t)

	tokens, err := c.Login("good")
	if err != nil {
		t.Fatal(err)
	}
	if tokens != (Tokens{Access: "tok", Refresh: "r1"}) {
		t.Errorf("tokens = %+v", tokens)
	}
	games, err := c.Games(tokens.Access)
	if err != nil {
		t.Fatal(err)
	}

	if g := games[0]; g.ImageURL != "http://img" || g.Source != "purchased" || g.TitleID != "T0" || g.StoreURL != "https://store.playstation.com/concept/100" {
		t.Errorf("first game = %+v", g)
	}
	if games[1].StoreURL != "" {
		t.Errorf("game without concept has store URL %q", games[1].StoreURL)
	}
	var tail []string
	for _, g := range games[pageSize:] {
		tail = append(tail, g.Name+"/"+g.Source)
	}
	// Pre-order, app, beta, demo and art book are left out; "Betamax" isn't a beta.
	if want := []string{"Plus Game/ps_plus", "Betamax Racer/purchased"}; !slices.Equal(tail, want) {
		t.Errorf("games after first page = %v, want %v", tail, want)
	}
}

func TestLoginBadNPSSO(t *testing.T) {
	c := fakePSN(t)
	if _, err := c.Login("bad"); err == nil || !strings.Contains(err.Error(), "NPSSO") {
		t.Fatalf("err = %v", err)
	}
}

func TestWriteCSV(t *testing.T) {
	var buf bytes.Buffer
	games := []Game{{Name: "Foo, the Game", Platform: "PS5", Source: "ps_plus", Genres: []string{"Action", "Racing"},
		Rating: 4.3, RatingCount: 12, LocalPlayers: 2, Accounts: []string{"kids", "main"}, TitleID: "T1",
		StoreURL: "https://s/1", MetacriticURL: "https://m/1"}}
	if err := writeGames(&buf, "csv", games); err != nil {
		t.Fatal(err)
	}
	want := "name,platform,source,genres,rating,rating_count,local_players,online_players,accounts,title_id,store_url,metacritic_url,image_url\n" +
		"\"Foo, the Game\",PS5,ps_plus,\"Action, Racing\",4.30,12,2,,\"kids, main\",T1,https://s/1,https://m/1,\n"
	if buf.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestWriteHTML(t *testing.T) {
	var buf bytes.Buffer
	games := []Game{
		{Name: "<b>Evil</b>", Platform: "PS5", Source: "purchased", Genres: []string{"Action", "Racing"},
			Rating: 4.32, RatingCount: 10, LocalPlayers: 1, OnlinePlayers: 4, Accounts: []string{"main"},
			StoreURL: "https://store/1", MetacriticURL: "https://mc/1"},
		{Name: "Calm", Platform: "PS4", Source: "ps_plus", Accounts: []string{"kids", "main"}},
	}
	if err := writeGames(&buf, "html", games); err != nil {
		t.Fatal(err)
	}
	page := buf.String()
	for _, want := range []string{
		"&lt;b&gt;Evil&lt;/b&gt;", // names are escaped
		`data-genre="Action|Racing"`,
		`data-account="kids|main"`,
		`<option>ps_plus</option>`,
		`<td class="num" data-value="4">1, online 4</td>`,
		"4.3★",
		`href="https://mc/1"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page is missing %s", want)
		}
	}
	if strings.Contains(page, "<b>Evil") {
		t.Error("name not escaped")
	}
}

func TestWriteJSONEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := writeGames(&buf, "json", nil); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "[]\n" {
		t.Errorf("got %q", buf.String())
	}
}
