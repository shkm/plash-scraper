# plash-scraper

Little tool for getting insight into your Playstation libraries. Supports multiple accounts, securely stores tokens in keychain.

## Install

```sh
go install github.com/shkm/plash-scraper@latest
```

## Usage

Add each account once:

```sh
plash-scraper accounts add
```

```sh
plash-scraper accounts list
plash-scraper accounts check                 # can each account still sign in?
plash-scraper accounts refresh <online-id>   # sign in again with a new NPSSO token
plash-scraper accounts remove <online-id>
```

List or search games on all accounts:

```sh
plash-scraper games list
plash-scraper games list -format csv -o games.csv
plash-scraper games search god of war
plash-scraper games search -format json zelda   # options go before the search text
plash-scraper games list -refresh               # skip the cache
```

Output formats: `table` (default), `json`, `csv`, `html`. One row per game, sorted by name.

Apps, demos and free items are recognised by a field in Sony's entitlement data (`featureType: 1`). It isn't documented, so this is based on what real accounts show. Bonus items bundled with a game, like the Horizon Zero Dawn art book, have no marker in Sony's data at all, so these are recognised by name: "beta", "demo", "trial", "art book" or "soundtrack", in either the library name or Sony's title name.

## Cache

Games are cached for 24 hours in the user cache directory (`~/Library/Caches/plash-scraper/` on macOS), so repeat runs and searches are instant. Pass `-refresh` to fetch from PlayStation anyway. Adding or removing an account also triggers a fresh fetch. The interactive view shows how old the data is.

## Storage

Tokens are stored in the OS keychain (macOS Keychain, Secret Service on Linux, Credential Manager on Windows) under the service `plash-scraper`, keyed by account ID. When PlayStation issues a new refresh token during a run, it replaces the stored one. Account IDs and online IDs are stored in `accounts.json` in the user config directory, for example `~/Library/Application Support/plash-scraper/` on macOS.

## Limits

- Only PS4 and PS5 games. The library API Sony uses doesn't return PS3 or Vita games.
- `source` doesn't say which PS Plus tier, or whether a game was a monthly game or from the catalog. The API doesn't expose that.
- This uses Sony's private API. If it stops returning games, the persisted query hash in `psn.go` may need updating. To find the new one, open [library.playstation.com](https://library.playstation.com) with the browser's dev tools open, find the `getPurchasedGameList` request, and copy the `sha256Hash` value.
