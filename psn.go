package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Client ID, secret and redirect URI belong to the official PlayStation mobile app.
const (
	clientID    = "09515159-7237-4370-9b40-3806e67c0891"
	basicAuth   = "Basic MDk1MTUxNTktNzIzNy00MzcwLTliNDAtMzgwNmU2N2MwODkxOnVjUGprYTV0bnRCMktxc1A="
	redirectURI = "com.scee.psxandroid.scecompcall://redirect"
	scope       = "psn:mobile.v2.core psn:clientapp"

	// Persisted query hash for getPurchasedGameList, as used by library.playstation.com.
	purchasedGamesHash  = "827a423f6a8ddca4107ac01395af2ec0eafd8396fc7fa204aaf9b7ed2eefa168"
	pageSize            = 100
	entitlementPageSize = 200
)

type Client struct {
	AuthURL         string
	GraphQLURL      string
	ProfileURL      string
	EntitlementsURL string
	CatalogURL      string
	HTTP            *http.Client
}

func NewClient() *Client {
	return &Client{
		AuthURL:         "https://ca.account.sony.com/api/authz/v3/oauth",
		GraphQLURL:      "https://web.np.playstation.com/api/graphql/v1/op",
		ProfileURL:      "https://us-prof.np.community.playstation.net/userProfile/v1/users",
		EntitlementsURL: "https://m.np.playstation.com/api/entitlement/v2/users/me/internal/entitlements",
		CatalogURL:      "https://m.np.playstation.com/api/catalog/v2/concepts",
		HTTP: &http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

type Game struct {
	Name          string   `json:"name"`
	Platform      string   `json:"platform"`
	Source        string   `json:"source"`
	Genres        []string `json:"genres"`
	Rating        float64  `json:"rating,omitempty"`
	RatingCount   int      `json:"ratingCount,omitempty"`
	LocalPlayers  int      `json:"localPlayers,omitempty"`
	OnlinePlayers int      `json:"onlinePlayers,omitempty"`
	Accounts      []string `json:"accounts"`
	TitleID       string   `json:"titleId"`
	ConceptID     string   `json:"conceptId,omitempty"`
	StoreURL      string   `json:"storeUrl"`
	MetacriticURL string   `json:"metacriticUrl"`
	ImageURL      string   `json:"imageUrl"`
}

// Stars formats the PlayStation Store rating, e.g. "4.3★". Empty if unrated.
func (g Game) Stars() string {
	if g.RatingCount == 0 {
		return ""
	}
	return fmt.Sprintf("%.1f★", g.Rating)
}

// Players describes how many can play, e.g. "1, online 4". Empty if unknown.
func (g Game) Players() string {
	switch {
	case g.OnlinePlayers == 0 && g.LocalPlayers == 0:
		return ""
	case g.OnlinePlayers == 0:
		return strconv.Itoa(g.LocalPlayers)
	case g.LocalPlayers == 0:
		return fmt.Sprintf("online %d", g.OnlinePlayers)
	default:
		return fmt.Sprintf("%d, online %d", g.LocalPlayers, g.OnlinePlayers)
	}
}

var trademarks = strings.NewReplacer("™", " ", "®", " ", "©", " ")

// metacriticURL links to a Metacritic search for the game (category 13 is
// games). Metacritic has no public API to find the game's own page.
func metacriticURL(name string) string {
	name = strings.Join(strings.Fields(trademarks.Replace(name)), " ")
	return "https://www.metacritic.com/search/" + url.PathEscape(name) + "/?category=13"
}

type apiGame struct {
	Name          string `json:"name"`
	Platform      string `json:"platform"`
	TitleID       string `json:"titleId"`
	EntitlementID string `json:"entitlementId"`
	Membership    string `json:"membership"`
	IsPreOrder    bool   `json:"isPreOrder"`
	Image         struct {
		URL string `json:"url"`
	} `json:"image"`
}

type Account struct {
	ID       string `json:"accountId"`
	OnlineID string `json:"onlineId"`
}

// Me returns the account the access token belongs to.
func (c *Client) Me(token string) (Account, error) {
	var body struct {
		Profile Account `json:"profile"`
	}
	if err := c.get(token, c.ProfileURL+"/me/profile2?fields=onlineId,accountId", &body); err != nil {
		return Account{}, fmt.Errorf("fetch profile: %w", err)
	}
	if body.Profile.ID == "" || body.Profile.OnlineID == "" {
		return Account{}, errors.New("fetch profile: no account ID or online ID in response")
	}
	return body.Profile, nil
}

type Tokens struct {
	Access  string
	Refresh string
}

// Login exchanges an NPSSO cookie for OAuth tokens. The NPSSO is tied to the
// browser session, so only the refresh token is worth keeping.
func (c *Client) Login(npsso string) (Tokens, error) {
	code, err := c.authorizationCode(npsso)
	if err != nil {
		return Tokens{}, err
	}
	tokens, err := c.token(url.Values{
		"code":         {code},
		"redirect_uri": {redirectURI},
		"grant_type":   {"authorization_code"},
		"token_format": {"jwt"},
	})
	if err == nil && tokens.Refresh == "" {
		err = errors.New("token exchange: no refresh token in response")
	}
	return tokens, err
}

// Refresh trades a refresh token for new tokens. Tokens.Refresh is empty if
// Sony didn't issue a new refresh token.
func (c *Client) Refresh(refresh string) (Tokens, error) {
	return c.token(url.Values{
		"refresh_token": {refresh},
		"grant_type":    {"refresh_token"},
		"token_format":  {"jwt"},
		"scope":         {scope},
	})
}

func (c *Client) authorizationCode(npsso string) (string, error) {
	q := url.Values{
		"access_type":   {"offline"},
		"client_id":     {clientID},
		"redirect_uri":  {redirectURI},
		"response_type": {"code"},
		"scope":         {scope},
	}
	req, err := http.NewRequest("GET", c.AuthURL+"/authorize?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Cookie", "npsso="+npsso)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	resp.Body.Close()

	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		return "", err
	}
	code := loc.Query().Get("code")
	if code == "" {
		return "", errors.New("NPSSO token rejected; it may have expired")
	}
	return code, nil
}

func (c *Client) token(form url.Values) (Tokens, error) {
	req, err := http.NewRequest("POST", c.AuthURL+"/token", strings.NewReader(form.Encode()))
	if err != nil {
		return Tokens{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", basicAuth)

	var body struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := c.doJSON(req, &body); err != nil {
		return Tokens{}, fmt.Errorf("token exchange: %w", err)
	}
	if body.AccessToken == "" {
		return Tokens{}, errors.New("token exchange: no access token in response")
	}
	return Tokens{Access: body.AccessToken, Refresh: body.RefreshToken}, nil
}

// Bonus items like art books have no marker in Sony's data, so they're
// recognised by name, as are betas and demos that aren't marked free.
var notAGame = regexp.MustCompile(`(?i)\b(beta|demo|trial|art ?book|soundtrack)\b`)

// Games returns the active PS4/PS5 games in the account's library, purchased
// or via PS Plus. Pre-orders, apps, demos, betas and bonus items are left out.
func (c *Client) Games(token string) ([]Game, error) {
	meta, err := c.entitlementMeta(token)
	if err != nil {
		return nil, err
	}
	var all []Game
	for start := 0; ; start += pageSize {
		page, isLast, err := c.gamesPage(token, start)
		if err != nil {
			return nil, err
		}
		for _, g := range page {
			m := meta[g.EntitlementID]
			if g.IsPreOrder || m.free || notAGame.MatchString(g.Name) || notAGame.MatchString(m.titleName) {
				continue
			}
			game := Game{
				Name:          g.Name,
				Platform:      g.Platform,
				TitleID:       g.TitleID,
				ConceptID:     m.conceptID,
				Source:        sourceName(g.Membership),
				MetacriticURL: metacriticURL(g.Name),
				ImageURL:      g.Image.URL,
			}
			if m.conceptID != "" {
				// Concept pages exist in every region; the store redirects to the visitor's.
				game.StoreURL = "https://store.playstation.com/concept/" + m.conceptID
			}
			all = append(all, game)
		}
		if isLast || len(page) == 0 {
			return all, nil
		}
	}
}

type conceptDetails struct {
	Genres        []string
	Rating        float64
	RatingCount   int
	LocalPlayers  int
	OnlinePlayers int
}

// ConceptDetails returns the genres, user rating and player counts from the
// store catalog. Concept IDs are the same in every region; GB gives English
// genre names.
func (c *Client) ConceptDetails(token, conceptID string) (conceptDetails, error) {
	var body struct {
		LocalizedGenres []struct {
			Value string `json:"value"`
		} `json:"localizedGenres"`
		// Sony sends these as strings, e.g. {"score":"4.32","total":"21547"}.
		StarRating struct {
			Score json.Number `json:"score"`
			Total json.Number `json:"total"`
		} `json:"starRating"`
		DefaultProduct struct {
			CompatibilityNotices []struct {
				Type  string `json:"type"`
				Value any    `json:"value"`
			} `json:"compatibilityNotices"`
		} `json:"defaultProduct"`
	}
	u := c.CatalogURL + "/" + url.PathEscape(conceptID) + "?age=99&country=GB&language=en"
	if err := c.get(token, u, &body); err != nil {
		return conceptDetails{}, fmt.Errorf("fetch concept %s: %w", conceptID, err)
	}

	var d conceptDetails
	if total, err := body.StarRating.Total.Int64(); err == nil && total > 0 {
		d.Rating, _ = body.StarRating.Score.Float64()
		d.RatingCount = int(total)
	}
	for _, g := range body.LocalizedGenres {
		if !slices.Contains(d.Genres, g.Value) {
			d.Genres = append(d.Genres, g.Value)
		}
	}
	for _, n := range body.DefaultProduct.CompatibilityNotices {
		count, ok := n.Value.(float64)
		if !ok {
			continue
		}
		switch {
		case n.Type == "NO_OF_PLAYERS":
			d.LocalPlayers = int(count)
		// Seen as NO_OF_NETWORK_PLAYERS_PS_PLUS: online play needs PS Plus.
		case strings.HasPrefix(n.Type, "NO_OF_NETWORK_PLAYERS"):
			d.OnlinePlayers = max(d.OnlinePlayers, int(count))
		}
	}
	return d, nil
}

type entitlementInfo struct {
	free      bool
	titleName string
	conceptID string
}

// entitlementMeta returns details the library API lacks, by entitlement ID.
func (c *Client) entitlementMeta(token string) (map[string]entitlementInfo, error) {
	meta := map[string]entitlementInfo{}
	for offset := 0; ; offset += entitlementPageSize {
		var body struct {
			TotalResults int `json:"totalResults"`
			Entitlements []struct {
				ID          string `json:"id"`
				FeatureType int    `json:"featureType"`
				TitleMeta   struct {
					Name string `json:"name"`
				} `json:"titleMeta"`
				ConceptMeta struct {
					ConceptID string `json:"conceptId"`
				} `json:"conceptMeta"`
			} `json:"entitlements"`
		}
		u := fmt.Sprintf("%s?fields=titleMeta,conceptMeta&limit=%d&offset=%d", c.EntitlementsURL, entitlementPageSize, offset)
		if err := c.get(token, u, &body); err != nil {
			return nil, fmt.Errorf("fetch entitlements: %w", err)
		}
		for _, e := range body.Entitlements {
			meta[e.ID] = entitlementInfo{
				// featureType isn't documented. On real accounts 1 marks apps
				// (Netflix, Spotify), demos and free-to-play games, while paid
				// and PS Plus games have 3.
				free:      e.FeatureType == 1,
				titleName: e.TitleMeta.Name,
				conceptID: e.ConceptMeta.ConceptID,
			}
		}
		if len(body.Entitlements) == 0 || offset+len(body.Entitlements) >= body.TotalResults {
			return meta, nil
		}
	}
}

func sourceName(membership string) string {
	if membership == "NONE" {
		return "purchased"
	}
	return strings.ToLower(membership)
}

// Don't filter by membership: Sony returns no games at all when it's set.
func (c *Client) gamesPage(token string, start int) ([]apiGame, bool, error) {
	variables, _ := json.Marshal(map[string]any{
		"isActive":      true,
		"platform":      []string{"ps4", "ps5"},
		"size":          pageSize,
		"start":         start,
		"sortBy":        "ACTIVE_DATE",
		"sortDirection": "asc",
	})
	extensions, _ := json.Marshal(map[string]any{
		"persistedQuery": map[string]any{"version": 1, "sha256Hash": purchasedGamesHash},
	})
	q := url.Values{
		"operationName": {"getPurchasedGameList"},
		"variables":     {string(variables)},
		"extensions":    {string(extensions)},
	}
	var body struct {
		Data struct {
			PurchasedTitlesRetrieve *struct {
				Games    []apiGame `json:"games"`
				PageInfo *struct {
					IsLast bool `json:"isLast"`
				} `json:"pageInfo"`
			} `json:"purchasedTitlesRetrieve"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := c.get(token, c.GraphQLURL+"?"+q.Encode(), &body); err != nil {
		return nil, false, fmt.Errorf("fetch games: %w", err)
	}
	if len(body.Errors) > 0 {
		return nil, false, fmt.Errorf("fetch games: %s", body.Errors[0].Message)
	}
	result := body.Data.PurchasedTitlesRetrieve
	if result == nil {
		return nil, false, errors.New("fetch games: empty response; the persisted query hash may be outdated")
	}
	isLast := result.PageInfo != nil && result.PageInfo.IsLast
	return result.Games, isLast, nil
}

func (c *Client) get(token, url string, v any) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	// The GraphQL server rejects requests without one as possible CSRF.
	req.Header.Set("Content-Type", "application/json")
	return c.doJSON(req, v)
}

func (c *Client) doJSON(req *http.Request, v any) error {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, data)
	}
	return json.Unmarshal(data, v)
}
