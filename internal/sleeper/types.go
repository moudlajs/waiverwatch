package sleeper

import "strings"

// User is a Sleeper account.
type User struct {
	UserID      string `json:"user_id"`
	Username    string `json:"username"` // lowercase login name
	DisplayName string `json:"display_name"`
}

// League is one fantasy league in one season.
type League struct {
	LeagueID        string         `json:"league_id"`
	PreviousID      string         `json:"previous_league_id"` // last season's league
	Name            string         `json:"name"`
	Season          string         `json:"season"`
	Status          string         `json:"status"` // pre_draft, drafting, in_season, complete
	TotalRosters    int            `json:"total_rosters"`
	RosterPositions []string       `json:"roster_positions"`
	Settings        LeagueSettings `json:"settings"`
	Scoring         Scoring        `json:"scoring_settings"`
}

// Scoring holds the scoring settings waiverwatch cares about.
type Scoring struct {
	Rec float64 `json:"rec"` // points per reception: 0, 0.5, 1...
}

// LeagueSettings holds the league settings waiverwatch cares about.
type LeagueSettings struct {
	Type             int `json:"type"` // see Kind
	PlayoffWeekStart int `json:"playoff_week_start"`
	PlayoffTeams     int `json:"playoff_teams"`
	MedianMatch      int `json:"league_average_match"` // 1: each week also a game against the league median
	WaiverType       int `json:"waiver_type"`          // 0 rolling priority, 1 reverse standings, 2 FAAB
	WaiverBudget     int `json:"waiver_budget"`
	DraftRounds      int `json:"draft_rounds"`
}

// Kind names the league format from settings.type.
func (l League) Kind() string {
	switch l.Settings.Type {
	case 0:
		return "redraft"
	case 1:
		return "keeper"
	case 2:
		return "dynasty"
	case 3:
		return "guillotine"
	default:
		return "unknown"
	}
}

// Roster is one team in a league; team defenses' IDs are team abbreviations (e.g. "SEA").
type Roster struct {
	RosterID int            `json:"roster_id"`
	OwnerID  string         `json:"owner_id"`
	CoOwners []string       `json:"co_owners"`
	Players  []string       `json:"players"`
	Starters []string       `json:"starters"`
	Reserve  []string       `json:"reserve"` // IR
	Taxi     []string       `json:"taxi"`
	Settings RosterSettings `json:"settings"`
}

// RosterSettings holds a team's record; Sleeper splits points into whole and hundredths parts.
type RosterSettings struct {
	Wins               int `json:"wins"`
	Losses             int `json:"losses"`
	Ties               int `json:"ties"`
	Fpts               int `json:"fpts"`
	FptsDecimal        int `json:"fpts_decimal"`
	FptsAgainst        int `json:"fpts_against"`
	FptsAgainstDecimal int `json:"fpts_against_decimal"`
	WaiverPosition     int `json:"waiver_position"`
	WaiverBudgetUsed   int `json:"waiver_budget_used"`
}

// PointsFor is the season's points scored.
func (s RosterSettings) PointsFor() float64 {
	return float64(s.Fpts) + float64(s.FptsDecimal)/100
}

// PointsAgainst is the season's points conceded.
func (s RosterSettings) PointsAgainst() float64 {
	return float64(s.FptsAgainst) + float64(s.FptsAgainstDecimal)/100
}

// LeagueUser is a member of a league.
type LeagueUser struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name"`
	Metadata    struct {
		TeamName string `json:"team_name"`
	} `json:"metadata"`
}

// Matchup is one team's side of a week's game; equal MatchupIDs play each other, 0 means no opponent.
type Matchup struct {
	RosterID       int                `json:"roster_id"`
	MatchupID      int                `json:"matchup_id"`
	Points         float64            `json:"points"`
	Starters       []string           `json:"starters"`
	StartersPoints []float64          `json:"starters_points"`
	PlayersPoints  map[string]float64 `json:"players_points"`
}

// State is the current point in the NFL calendar.
type State struct {
	Season     string `json:"season"`
	SeasonType string `json:"season_type"` // pre, regular, post
	Week       int    `json:"week"`
}

// Trending is a player's add (or drop) count over the lookback window.
type Trending struct {
	PlayerID string `json:"player_id"`
	Count    int    `json:"count"`
}

// Player is a dictionary entry; team defenses use the team abbreviation as ID and have no FullName.
type Player struct {
	PlayerID       string `json:"player_id"`
	FullName       string `json:"full_name"`
	FirstName      string `json:"first_name"`
	LastName       string `json:"last_name"`
	Position       string `json:"position"`
	Team           string `json:"team"` // empty for free agents
	Status         string `json:"status"`
	Active         bool   `json:"active"`
	InjuryStatus   string `json:"injury_status"` // Questionable, Doubtful, Out, IR, PUP, Sus, NA...
	InjuryBodyPart string `json:"injury_body_part"`
	SearchRank     int    `json:"search_rank"` // Sleeper's overall rank, 1 is best; 0 when unranked
	Age            int    `json:"age"`         // 0 when unknown (and for team defenses)
}

// Name is the display name, e.g. "Patrick Mahomes" or "Seattle Seahawks".
func (p Player) Name() string {
	if p.FullName != "" {
		return p.FullName
	}
	return strings.TrimSpace(p.FirstName + " " + p.LastName)
}

// Draft is one draft in a league (a startup, a rookie draft, a redraft).
type Draft struct {
	DraftID  string `json:"draft_id"`
	Season   string `json:"season"`
	Status   string `json:"status"` // pre_draft, drafting, paused, complete
	Type     string `json:"type"`   // snake, linear, auction
	Settings struct {
		Rounds int `json:"rounds"`
		Teams  int `json:"teams"`
	} `json:"settings"`
}

// Pick is one selection in a draft: PickedBy is the user, RosterID the team within that season.
type Pick struct {
	Round    int    `json:"round"`
	PickNo   int    `json:"pick_no"`
	PlayerID string `json:"player_id"`
	PickedBy string `json:"picked_by"`
	RosterID int    `json:"roster_id"`
	IsKeeper bool   `json:"is_keeper"`
	Metadata struct {
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
		Position  string `json:"position"`
		Team      string `json:"team"`
		Amount    string `json:"amount"` // auction drafts: the price paid
	} `json:"metadata"`
}

// TradedPick is a pick that changed hands: RosterID is its original team, OwnerID the current holder.
type TradedPick struct {
	Season          string `json:"season"`
	Round           int    `json:"round"`
	RosterID        int    `json:"roster_id"`
	OwnerID         int    `json:"owner_id"`
	PreviousOwnerID int    `json:"previous_owner_id"`
}
