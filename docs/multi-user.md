# Multi-user design

Decided 2026-09-26 (#52). waiverwatch goes from "the owner's leagues" to
"anyone's leagues", at no running cost and with nothing personal stored.

## Identity: the Sleeper username

Sleeper data is public: anyone can look up any user's leagues by username.
So identity only has to answer "which Sleeper user do I answer for?", not
"who is this person?".

- The OAuth sign-in page (already ours, `internal/auth`) asks for a **Sleeper
  username** instead of the owner passphrase, and checks it exists.
- Codes and tokens carry the Sleeper **user id and username**. They stay
  stateless and HMAC-signed.
- Typing someone else's username shows only what Sleeper already shows
  anyone. Accepted.

Rejected: *Sign in with Google.* It needs an OAuth client set up by hand in
the console, stores emails, and needs a database mapping accounts to Sleeper
usernames, all to protect data that is public anyway.

## Access: anyone, with an emergency allowlist

- Open to every Sleeper user.
- `WAIVERWATCH_ALLOWED_USERS` (comma-separated usernames) restricts sign-in
  when set; unset (the repository variable deleted, since GitHub doesn't
  allow empty ones) means anyone. For abuse, not for day-to-day use.

Rejected: an invite list. Adding people one by one doesn't fit a public post,
and email invites would need an email service.

## Storage: none

Tokens carry the username, so there is nothing to store. No Firestore, no
personal data, nothing to delete on request. (#54 is only plumbing the token's
username into the tools; #55, a first-run step, is not needed.)

## Limits and cost

| Limit | Why |
|---|---|
| Per user: 30 tool calls a minute, burst 10 | one heavy user can't starve the rest |
| Global: calls to Sleeper, 600/min (10/s, burst 50) | Sleeper asks for < 1000/min per IP and may block above it; every user shares Cloud Run's egress, and one tool call makes 10-30 Sleeper calls |
| Per instance: 5 req/s (exists) | backstop |
| Billing kill switch at 25 CZK/month (exists) | last line |

Free tier: 2M requests and 180k vCPU-seconds a month. A tool call takes
roughly 0.1-0.5 CPU-seconds, so hundreds of thousands of calls a month fit.
The Sleeper budget binds long before cost does (~20-60 tool calls a minute
across everyone); `max-instances` can go from 1 to 2-3 if people wait on
each other.

That 20-60 a minute is a **ceiling, not a free-tier guarantee**: sustained
around the clock for a month it would be up to ~2.6M tool calls and roughly
0.8-1.3M vCPU-seconds, 4-7x the free tier (Cloud Run bills the whole request,
including time spent waiting on Sleeper). Realistic use is a small fraction
of that; per-user limits keep one user from driving it, and the billing kill
switch bounds the worst case at the budget plus a few hours.

## Privacy

waiverwatch stores nothing. Cloud Run's standard request logs keep IP
addresses, paths and status codes (30 days by default); usernames are not
logged (they travel in the sign-in form body and inside signed tokens). The
public guide (#57) says so.

## Order

#53 sign-in with a Sleeper username → #54 tools answer for the token's user →
#56 limits → #57 public guide and Reddit post.
