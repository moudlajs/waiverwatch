package league

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/moudlajs/waiverwatch/internal/sleeper"
)

// fanOut caps concurrent requests to Sleeper when working across leagues.
const fanOut = 8

// Service answers questions about one user's leagues. Every call fetches
// live data from Sleeper; only the player dictionary is cached.
type Service struct {
	api      *sleeper.Client
	players  *Directory
	username string
}

// NewService returns a Service for the Sleeper user username.
func NewService(api *sleeper.Client, players *Directory, username string) *Service {
	return &Service{api: api, players: players, username: username}
}

// Overview is the user's leagues for the current season and week.
type Overview struct {
	Season  string    `json:"season"`
	Week    int       `json:"week"`
	Leagues []Summary `json:"leagues"`
}

// Summary is the user's position in one league.
type Summary struct {
	LeagueID      string  `json:"league_id"`
	Name          string  `json:"name"`
	Kind          string  `json:"kind" jsonschema:"redraft, keeper, dynasty or guillotine (survival: lowest score each week is eliminated)"`
	Status        string  `json:"status"`
	Teams         int     `json:"teams"`
	MyTeam        string  `json:"my_team,omitempty"`
	Wins          int     `json:"wins"`
	Losses        int     `json:"losses"`
	Ties          int     `json:"ties"`
	PointsFor     float64 `json:"points_for"`
	PointsAgainst float64 `json:"points_against"`
	Standing      int     `json:"standing,omitempty" jsonschema:"1 is first. By wins then points, or by points in guillotine leagues"`
	Error         string  `json:"error,omitempty" jsonschema:"set when this league could not be loaded; the others are still valid"`
}

// Overview lists every league the user is in this season with their record
// and standing. A league that fails to load carries an Error instead of
// failing the whole answer.
func (s *Service) Overview(ctx context.Context) (Overview, error) {
	state, user, leagues, err := s.myLeagues(ctx)
	if err != nil {
		return Overview{}, err
	}
	out := Overview{Season: state.Season, Week: state.Week, Leagues: make([]Summary, len(leagues))}
	eachLeague(leagues, func(i int, l sleeper.League) {
		sum, err := s.summarise(ctx, l, user.UserID)
		if err != nil {
			sum.Error = err.Error()
		}
		out.Leagues[i] = sum
	})
	return out, nil
}

// myLeagues loads the current NFL state, the user, and their leagues for the
// current season.
func (s *Service) myLeagues(ctx context.Context) (sleeper.State, sleeper.User, []sleeper.League, error) {
	state, err := s.api.State(ctx)
	if err != nil {
		return sleeper.State{}, sleeper.User{}, nil, err
	}
	user, err := s.api.User(ctx, s.username)
	if err != nil {
		return sleeper.State{}, sleeper.User{}, nil, err
	}
	leagues, err := s.api.Leagues(ctx, user.UserID, state.Season)
	if err != nil {
		return sleeper.State{}, sleeper.User{}, nil, err
	}
	return state, user, leagues, nil
}

// eachLeague runs fn for every league concurrently, at most fanOut at once.
// fn reports its own errors; each call must only write to its own index.
func eachLeague(leagues []sleeper.League, fn func(i int, l sleeper.League)) {
	var g errgroup.Group
	g.SetLimit(fanOut)
	for i, l := range leagues {
		g.Go(func() error { fn(i, l); return nil })
	}
	_ = g.Wait() // fn never returns an error; see above
}

func (s *Service) summarise(ctx context.Context, l sleeper.League, userID string) (Summary, error) {
	sum := Summary{
		LeagueID: l.LeagueID,
		Name:     l.Name,
		Kind:     l.Kind(),
		Status:   l.Status,
		Teams:    l.TotalRosters,
	}

	var (
		rosters []sleeper.Roster
		users   []sleeper.LeagueUser
	)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() (err error) { rosters, err = s.api.Rosters(gctx, l.LeagueID); return err })
	g.Go(func() (err error) { users, err = s.api.LeagueUsers(gctx, l.LeagueID); return err })
	if err := g.Wait(); err != nil {
		return sum, err
	}

	mine, ok := MyRoster(rosters, userID)
	if !ok {
		return sum, fmt.Errorf("no roster owned by user %s in league %s", userID, l.LeagueID)
	}
	st := mine.Settings
	sum.MyTeam = TeamName(users, mine.OwnerID)
	sum.Wins, sum.Losses, sum.Ties = st.Wins, st.Losses, st.Ties
	sum.PointsFor, sum.PointsAgainst = st.PointsFor(), st.PointsAgainst()
	sum.Standing = Standing(rosters, mine.RosterID, l.Kind() == "guillotine")
	return sum, nil
}

// MyRoster finds the roster userID owns or co-owns.
func MyRoster(rosters []sleeper.Roster, userID string) (sleeper.Roster, bool) {
	for _, r := range rosters {
		if r.OwnerID == userID || slices.Contains(r.CoOwners, userID) {
			return r, true
		}
	}
	return sleeper.Roster{}, false
}

// TeamName is the owner's team name, falling back to their display name.
func TeamName(users []sleeper.LeagueUser, ownerID string) string {
	for _, u := range users {
		if u.UserID == ownerID {
			if name := strings.TrimSpace(u.Metadata.TeamName); name != "" {
				return name
			}
			return u.DisplayName
		}
	}
	return ""
}

// Standing is the 1-based rank of rosterID: by wins, then fewer losses, then
// points for; by points for alone when byPoints (guillotine leagues, where
// the weekly record means nothing). Tied teams share a rank. 0 if rosterID
// is not in rosters.
func Standing(rosters []sleeper.Roster, rosterID int, byPoints bool) int {
	var me *sleeper.Roster
	for i := range rosters {
		if rosters[i].RosterID == rosterID {
			me = &rosters[i]
		}
	}
	if me == nil {
		return 0
	}
	rank := 1
	for _, r := range rosters {
		if ahead(r.Settings, me.Settings, byPoints) {
			rank++
		}
	}
	return rank
}

// ahead reports whether a ranks strictly above b.
func ahead(a, b sleeper.RosterSettings, byPoints bool) bool {
	if !byPoints {
		if a.Wins != b.Wins {
			return a.Wins > b.Wins
		}
		if a.Losses != b.Losses {
			return a.Losses < b.Losses
		}
	}
	return a.PointsFor() > b.PointsFor()
}
