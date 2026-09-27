#!/usr/bin/env bash
# Usage summary for the hosted waiverwatch, from its anonymous usage log.
#
#   deploy/stats.sh [days] [project-id]     (default: 7 days, waiverwatch-509716)
#
# Per UTC day: distinct users (anonymous daily IDs), tool calls, errors,
# sign-ins by outcome, and the most used tools. No usernames exist in the
# log, so none can be shown.
set -euo pipefail

DAYS=${1:-7}
PROJECT=${2:-waiverwatch-509716}

gcloud logging read \
  "resource.type=\"cloud_run_revision\" AND resource.labels.service_name=\"waiverwatch\" AND (jsonPayload.message=\"tool call\" OR jsonPayload.message=\"sign in\")" \
  --project "$PROJECT" --freshness "${DAYS}d" --limit 100000 --format json |
  jq -r '
    map({day: .timestamp[0:10], p: .jsonPayload})
    | group_by(.day)
    | map(
        (map(select(.p.message == "tool call"))) as $calls
        | (map(select(.p.message == "sign in"))) as $signins
        | {
            day: .[0].day,
            users: ($calls | map(.p.user) | map(select(. != "" and . != null)) | unique | length),
            calls: ($calls | length),
            errors: ($calls | map(select(.p.ok == false)) | length),
            signins: ($signins | map(select(.p.outcome == "ok")) | length),
            refused: ($signins | map(select(.p.outcome != "ok")) | length),
            top: ($calls | group_by(.p.tool) | map({t: .[0].p.tool, n: length}) | sort_by(-.n) | .[0:3]
                  | map("\(.t) \(.n)") | join(", "))
          })
    | if length == 0 then "no usage logged in this period"
      else (["DAY", "USERS", "CALLS", "ERRORS", "SIGN-INS", "REFUSED", "TOP TOOLS"] | @tsv),
           (.[] | [.day, .users, .calls, .errors, .signins, .refused, .top] | @tsv)
      end
  ' | column -t -s $'\t'
