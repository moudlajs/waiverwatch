# What you can ask

Every question covers all your leagues unless you name one ("…in my dynasty
league"); Claude matches league names loosely.

## Game day

- *"How are my matchups this week?"* Live and projected scores, starter by
  starter, for you and your opponent. Survival (guillotine) leagues show your
  rank and how far you are above last place, plus where Sleeper's projections
  put you this week, with a warning when you're projected in the bottom two.
- *"Is my lineup right in every league?"* Empty slots, starters who are out or
  on bye, and bench players projected to beat a starter, with the changes to
  make and the points they're worth.
- *"Who's hurt on my teams, and who can I pick up instead?"*
- *"Compare me with my opponent in my 12-team league."*
- *"How did last week go, and what did I leave on my bench?"* Wins and
  losses everywhere, your top scorer, the best lineup in hindsight, who
  should have started, and losses a better lineup would have won.

## Waivers

- *"Who should I pick up at RB, and how much should I bid?"* The best free
  agents you can actually start in each league, ranked by rest-of-season
  value and this week's projection (survival leagues: this week first), with
  your waiver priority, or your FAAB left and a suggested bid.
- *"What's trending on waivers, and where is he still available?"*
- *"Where am I thin? Any position without a backup?"*

## Trades

Values from [FantasyCalc](https://fantasycalc.com), built from real fantasy
trades and fitted to each league: dynasty or redraft, superflex or 1QB, league
size, PPR.

- *"What's my roster worth in my dynasty league, and where do I rank?"*
- *"Is Gibbs for Chase and a 2027 1st fair?"* Both sides valued, adjusted so
  two good players don't automatically beat one great one, with who it leans
  to and your depth before and after. Draft picks are checked against who
  actually holds them; next year's picks are valued early, mid or late from
  the original team's record.
- *"Who should I trade for?"* Your thin positions, the spare players you can
  afford to give up, and players on other teams they can buy. Deals that also
  fill the other team's thin spot come first: those get accepted.

## Season

- *"What are my playoff chances?"* Per league: your remaining games with the
  chance to win each, whether the schedule is harder or easier than average,
  expected wins and a playoff percentage from 5,000 simulated seasons.

## History

- *"Where did I draft Kenneth Walker?"* Dynasty leagues include the startup
  draft and past rookie drafts.

## Tools

| Tool | Returns |
|---|---|
| `list_leagues` | every league this season: type, size, your team, record, points, standing |
| `get_matchups` | this week (or any week) in every league: live and projected score, your and your opponent's starters with injuries and projections; guillotine rank and margin over last place, and the projected cut line |
| `lineup_check` | per league this week: empty slots, starters out or on bye, questionable starters, and the best lineup by projections (flex-aware) as start/bench changes |
| `get_roster` | a team's starters (by slot), bench, IR and taxi with injuries: yours, or any owner by name |
| `compare_rosters` | your roster next to this week's opponent (or any owner), position by position, with records and trade values (totals, starters, per position) |
| `draft_results` | your picks in every league (dynasty: startup and rookie drafts too); "where did I draft X?" |
| `position_depth` | per league: starting slots (flex-aware) vs healthy players and backups; every thin spot across leagues |
| `injury_report` | your injured players across all leagues, where they start for you, and a free replacement in each of those leagues |
| `player_values` | trade values (FantasyCalc, from real trades) fitted to each league's format: named players and who has them, or a whole roster with its total and the league's team value ranking |
| `evaluate_trade` | "is this trade fair?": both sides valued for that league (players, and dynasty picks checked against who holds them), 2-for-1 adjusted, who it leans to and by how much, your depth before and after |
| `trade_targets` | per league: your thin positions, the spare players you can afford to trade, and players on other teams they can buy, each with the cheapest offer |
| `season_outlook` | per head-to-head league: remaining schedule with win chances, schedule difficulty, expected wins and playoff chance (simulated) |
| `trending_players` | the most-added players on Sleeper, with the leagues where you can still claim each one |
| `week_recap` | a finished week (last week by default) in every league: result, top scorer, points left on the bench with the swaps that would have scored them, and losses the best lineup would have won |
| `waiver_targets` | per league: the best free agents you can actually start there, ranked by trade value and projection, your waiver priority or FAAB left, and a suggested FAAB bid |
