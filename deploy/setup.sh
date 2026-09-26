#!/usr/bin/env bash
# One-time (and safely re-runnable) Google Cloud setup for waiverwatch.
#
# Creates what the release workflow needs to deploy to Cloud Run, with no
# keys stored anywhere: GitHub Actions signs in through Workload Identity
# Federation, only from this repository's main branch. Also creates the
# OAuth sign-in secrets, readable only by the runtime service account.
#
# Prerequisites: a project with billing linked, `gcloud auth login` done,
# and `gh auth login` for writing the repository variables.
#
#   deploy/setup.sh <project-id> <billing-account-id>
set -euo pipefail

PROJECT=${1:?usage: deploy/setup.sh <project-id> <billing-account-id>}
BILLING=${2:?usage: deploy/setup.sh <project-id> <billing-account-id>}
REPO=moudlajs/waiverwatch
REGION=europe-west1          # Belgium: closest Cloud Run region with free tier pricing to Prague
AR_REPO=waiverwatch          # Artifact Registry repository
RUNTIME_SA=waiverwatch-run   # identity the service runs as: no project roles, reads only its two secrets
DEPLOY_SA=github-deploy      # identity GitHub Actions deploys as
POOL=github
PROVIDER=github-oidc
BUDGET_CZK=25                # about $1: any spend at all means something is wrong

gc() { gcloud --project "$PROJECT" --quiet "$@"; }
say() { printf '\n== %s\n' "$*"; }

# retry runs a command up to 6 times, 10s apart. New service accounts and
# freshly enabled APIs take a while to be usable in IAM policies.
retry() {
  local n
  for n in 1 2 3 4 5 6; do
    "$@" && return 0
    echo "  (not ready yet, retrying in 10s: attempt $n/6)" >&2
    sleep 10
  done
  "$@"
}

NUMBER=$(gcloud projects describe "$PROJECT" --format='value(projectNumber)')
RUNTIME_EMAIL="$RUNTIME_SA@$PROJECT.iam.gserviceaccount.com"
DEPLOY_EMAIL="$DEPLOY_SA@$PROJECT.iam.gserviceaccount.com"
POOL_ID="projects/$NUMBER/locations/global/workloadIdentityPools/$POOL"

say "APIs"
gc services enable \
  run.googleapis.com \
  artifactregistry.googleapis.com \
  iam.googleapis.com \
  iamcredentials.googleapis.com \
  sts.googleapis.com \
  secretmanager.googleapis.com \
  pubsub.googleapis.com \
  cloudbilling.googleapis.com \
  billingbudgets.googleapis.com

say "Artifact Registry: $AR_REPO in $REGION (keeps the 5 newest images)"
if ! gc artifacts repositories describe "$AR_REPO" --location "$REGION" >/dev/null 2>&1; then
  retry gc artifacts repositories create "$AR_REPO" --location "$REGION" --repository-format docker \
    --description "waiverwatch images"
fi
policy=$(mktemp)
trap 'rm -f "$policy"' EXIT
cat >"$policy" <<'JSON'
[
  {"name": "keep-newest-5", "action": {"type": "Keep"}, "mostRecentVersions": {"keepCount": 5}},
  {"name": "delete-older", "action": {"type": "Delete"}, "condition": {"tagState": "ANY"}}
]
JSON
gc artifacts repositories set-cleanup-policies "$AR_REPO" --location "$REGION" --policy "$policy" --no-dry-run >/dev/null

say "Service accounts"
for sa in "$RUNTIME_SA:waiverwatch runtime" "$DEPLOY_SA:GitHub Actions deploys"; do
  name=${sa%%:*}
  if ! gc iam service-accounts describe "$name@$PROJECT.iam.gserviceaccount.com" >/dev/null 2>&1; then
    gc iam service-accounts create "$name" --display-name "${sa#*:}"
  fi
done

say "Deployer permissions"
# run.admin (not run.developer) because making the service public needs setIamPolicy.
retry gc projects add-iam-policy-binding "$PROJECT" --member "serviceAccount:$DEPLOY_EMAIL" \
  --role roles/run.admin --condition None >/dev/null
retry gc artifacts repositories add-iam-policy-binding "$AR_REPO" --location "$REGION" \
  --member "serviceAccount:$DEPLOY_EMAIL" --role roles/artifactregistry.writer >/dev/null
# Deploying a service that runs as RUNTIME_SA requires acting as it.
retry gc iam service-accounts add-iam-policy-binding "$RUNTIME_EMAIL" \
  --member "serviceAccount:$DEPLOY_EMAIL" --role roles/iam.serviceAccountUser >/dev/null

say "Secrets (OAuth sign-in, #34)"
# Both are random and generated once, straight into Secret Manager: nothing
# is printed. The owner copies the passphrase into a password manager with
# the command below; nobody needs to see the signing key.
for secret in waiverwatch-signing-key waiverwatch-passphrase; do
  if ! gc secrets describe "$secret" >/dev/null 2>&1; then
    gc secrets create "$secret" --replication-policy automatic
  fi
  retry gc secrets add-iam-policy-binding "$secret" \
    --member "serviceAccount:$RUNTIME_EMAIL" --role roles/secretmanager.secretAccessor >/dev/null
done
if [ -z "$(gc secrets versions list waiverwatch-signing-key --filter state=enabled --format 'value(name)')" ]; then
  openssl rand -base64 48 | tr -d '\n' | gc secrets versions add waiverwatch-signing-key --data-file=- >/dev/null
  echo "  generated a signing key"
fi
if [ -z "$(gc secrets versions list waiverwatch-passphrase --filter state=enabled --format 'value(name)')" ]; then
  # Exactly 30 letters and digits (~178 bits), so it pastes cleanly. tr is
  # read through < <(…): as a pipeline stage its SIGPIPE when head stops
  # reading would fail the pipeline under pipefail and abort this script.
  head -c 30 < <(LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom) |
    gc secrets versions add waiverwatch-passphrase --data-file=- >/dev/null
  echo "  generated a sign-in passphrase"
fi
cat <<MSG
  To copy the sign-in passphrase to the clipboard (for a password manager):
    gcloud secrets versions access latest --secret waiverwatch-passphrase --project $PROJECT | pbcopy
MSG

say "Workload Identity Federation for $REPO (main branch only)"
if ! gc iam workload-identity-pools describe "$POOL" --location global >/dev/null 2>&1; then
  gc iam workload-identity-pools create "$POOL" --location global --display-name "GitHub Actions"
fi
if ! gc iam workload-identity-pools providers describe "$PROVIDER" --location global \
  --workload-identity-pool "$POOL" >/dev/null 2>&1; then
  gc iam workload-identity-pools providers create-oidc "$PROVIDER" --location global \
    --workload-identity-pool "$POOL" --display-name "GitHub OIDC" \
    --issuer-uri "https://token.actions.githubusercontent.com" \
    --attribute-mapping "google.subject=assertion.sub,attribute.repository=assertion.repository,attribute.ref=assertion.ref" \
    --attribute-condition "assertion.repository == '$REPO' && assertion.ref == 'refs/heads/main'"
fi
retry gc iam service-accounts add-iam-policy-binding "$DEPLOY_EMAIL" \
  --role roles/iam.workloadIdentityUser \
  --member "principalSet://iam.googleapis.com/$POOL_ID/attribute.repository/$REPO" >/dev/null

say "Budget alert: $BUDGET_CZK CZK/month on $PROJECT"
budgets=$(gcloud billing budgets list --billing-account "$BILLING" --format='value(displayName)')
if ! grep -qx waiverwatch <<<"$budgets"; then
  gcloud billing budgets create --billing-account "$BILLING" --display-name waiverwatch \
    --budget-amount "${BUDGET_CZK}CZK" --filter-projects "projects/$PROJECT" \
    --threshold-rule percent=0.5 --threshold-rule percent=1.0
fi

say "Billing kill switch (#51)"
# The budget publishes to TOPIC; a push subscription delivers each message
# to the private waiverwatch-killswitch service (only PUSH_SA may invoke
# it). When actual cost reaches the budget, it unlinks the project's
# billing, which stops every paid service. KILLSWITCH_DRY_RUN=1 deploys it
# in log-only mode. It runs the latest release's image, so run this after
# a release that contains /killswitch.
TOPIC=billing-budget
KILL_SA=killswitch
PUSH_SA=killswitch-push
KILL_EMAIL="$KILL_SA@$PROJECT.iam.gserviceaccount.com"
PUSH_EMAIL="$PUSH_SA@$PROJECT.iam.gserviceaccount.com"
for sa in "$KILL_SA:billing kill switch" "$PUSH_SA:Pub/Sub push to the kill switch"; do
  name=${sa%%:*}
  if ! gc iam service-accounts describe "$name@$PROJECT.iam.gserviceaccount.com" >/dev/null 2>&1; then
    gc iam service-accounts create "$name" --display-name "${sa#*:}"
  fi
done
# Unlinking billing needs resourcemanager.projects.deleteBillingAssignment,
# which Project Billing Manager on this project grants. Nothing on the
# billing account itself.
# Reading billing info first needs resourcemanager.projects.get, which only
# roles/browser (read-only project metadata) adds.
for role in roles/billing.projectManager roles/browser; do
  retry gc projects add-iam-policy-binding "$PROJECT" --member "serviceAccount:$KILL_EMAIL" \
    --role "$role" --condition None >/dev/null
done

if ! gc pubsub topics describe "$TOPIC" >/dev/null 2>&1; then
  gc pubsub topics create "$TOPIC"
fi
# Cloud Billing publishes budget notifications as this Google-managed account.
retry gc pubsub topics add-iam-policy-binding "$TOPIC" \
  --member serviceAccount:billing-budget-alert@system.gserviceaccount.com \
  --role roles/pubsub.publisher >/dev/null

TAG=$(gh release view --repo "$REPO" --json tagName --jq .tagName)
gc run deploy waiverwatch-killswitch --region "$REGION" \
  --image "$REGION-docker.pkg.dev/$PROJECT/$AR_REPO/waiverwatch:$TAG" \
  --command /killswitch --service-account "$KILL_EMAIL" --no-allow-unauthenticated \
  --min-instances 0 --max-instances 1 --cpu 1 --memory 256Mi --timeout 60 \
  --set-env-vars "KILLSWITCH_PROJECT=$PROJECT,KILLSWITCH_DRY_RUN=${KILLSWITCH_DRY_RUN:-0}" >/dev/null
KILL_URL=$(gc run services describe waiverwatch-killswitch --region "$REGION" --format 'value(status.url)')
retry gc run services add-iam-policy-binding waiverwatch-killswitch --region "$REGION" \
  --member "serviceAccount:$PUSH_EMAIL" --role roles/run.invoker >/dev/null
if ! gc pubsub subscriptions describe killswitch >/dev/null 2>&1; then
  retry gc pubsub subscriptions create killswitch --topic "$TOPIC" \
    --push-endpoint "$KILL_URL" --push-auth-service-account "$PUSH_EMAIL" \
    --ack-deadline 60 --message-retention-duration 1d
fi
budget=$(gcloud billing budgets list --billing-account "$BILLING" \
  --filter 'displayName=waiverwatch' --format 'value(name)')
gcloud billing budgets update "$budget" --billing-account "$BILLING" \
  --notifications-rule-pubsub-topic "projects/$PROJECT/topics/$TOPIC" >/dev/null
echo "  kill switch $TAG at $KILL_URL (dry run: ${KILLSWITCH_DRY_RUN:-0})"

say "GitHub repository variables"
gh variable set GCP_PROJECT_ID --repo "$REPO" --body "$PROJECT"
gh variable set GCP_REGION --repo "$REPO" --body "$REGION"
gh variable set GCP_WIF_PROVIDER --repo "$REPO" --body "$POOL_ID/providers/$PROVIDER"
gh variable set GCP_DEPLOY_SA --repo "$REPO" --body "$DEPLOY_EMAIL"
gh variable set GCP_RUNTIME_SA --repo "$REPO" --body "$RUNTIME_EMAIL"
gh variable set GCP_IMAGE --repo "$REPO" --body "$REGION-docker.pkg.dev/$PROJECT/$AR_REPO/waiverwatch"

say "Done"
