// Command waiverwatch lists a Sleeper user's NFL leagues.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const base = "https://api.sleeper.app/v1"

type User struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name"`
}

type League struct {
	LeagueID string `json:"league_id"`
	Name     string `json:"name"`
	Season   string `json:"season"`
	Status   string `json:"status"`
	Sport    string `json:"sport"`
}

var client = &http.Client{Timeout: 10 * time.Second}

// get fetches a URL and decodes JSON into dst. Generics keep this to one function.
func get[T any](ctx context.Context, url string, dst *T) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(dst)
}

func main() {
	ctx := context.Background()

	var u User
	if err := get(ctx, base+"/user/Moudlajs", &u); err != nil {
		panic(err)
	}
	fmt.Printf("%s (%s)\n\n", u.DisplayName, u.UserID)

	var leagues []League
	if err := get(ctx, fmt.Sprintf("%s/user/%s/leagues/nfl/2026", base, u.UserID),
		&leagues); err != nil {
		panic(err)
	}
	for _, l := range leagues {
		fmt.Printf("  %-30s %s (%s)\n", l.Name, l.Season, l.Status)
	}
}
