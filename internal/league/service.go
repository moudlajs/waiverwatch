package league

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/moudlajs/waiverwatch/internal/sleeper"
)

const fanOut = 8

// Service answers questions about a Sleeper user's leagues.
type Service struct {
	api         *sleeper.Client
	players     *Directory
	values      FetchValues
	fallback    FetchValues
	projections projectionCache
	defaultUser string
}

// NewService returns a Service answering for the user set by WithUser, else defaultUser.
func NewService(api *sleeper.Client, players *Directory, values FetchValues, defaultUser string) *Service {
	return &Service{api: api, players: players, values: values, defaultUser: defaultUser}
}

type userKey struct{}

// WithUser makes Service calls under ctx answer for this Sleeper username.
func WithUser(ctx context.Context, username string) context.Context {
	return context.WithValue(ctx, userKey{}, username)
}

func (s *Service) user(ctx context.Context) (string, error) {
	if u, _ := ctx.Value(userKey{}).(string); u != "" {
		return u, nil
	}
	if s.defaultUser != "" {
		return s.defaultUser, nil
	}
	return "", errors.New("no Sleeper user for this request")
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

// Overview lists the user's leagues this season with their record and standing.
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

func (s *Service) myLeagues(ctx context.Context) (sleeper.State, sleeper.User, []sleeper.League, error) {
	state, err := s.api.State(ctx)
	if err != nil {
		return sleeper.State{}, sleeper.User{}, nil, err
	}
	username, err := s.user(ctx)
	if err != nil {
		return sleeper.State{}, sleeper.User{}, nil, err
	}
	user, err := s.api.User(ctx, username)
	if errors.Is(err, sleeper.ErrNotFound) {
		return sleeper.State{}, sleeper.User{}, nil, fmt.Errorf("sleeper has no user named %q: %w", username, err)
	}
	if err != nil {
		return sleeper.State{}, sleeper.User{}, nil, err
	}
	leagues, err := s.api.Leagues(ctx, user.UserID, state.Season)
	if err != nil {
		return sleeper.State{}, sleeper.User{}, nil, err
	}
	return state, user, leagues, nil
}

// eachLeague runs fn per league, fanOut at a time; each call must only write to its own index.
func eachLeague(leagues []sleeper.League, fn func(i int, l sleeper.League)) {
	var g errgroup.Group
	g.SetLimit(fanOut)
	for i, l := range leagues {
		g.Go(func() error { fn(i, l); return nil })
	}
	_ = g.Wait() // fn never returns an error
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

// Standing is the 1-based rank of rosterID (by points alone when byPoints); ties share a rank, 0 if absent.
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
