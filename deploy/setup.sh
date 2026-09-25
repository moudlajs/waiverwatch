#!/usr/bin/env bash
# One-time (and safely re-runnable) Google Cloud setup for waiverwatch.
#
# Creates what the release workflow needs to deploy to Cloud Run, with no
# keys stored anywhere: GitHub Actions signs in through Workload Identity
# Federation, only from this repository's main branch.
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
RUNTIME_SA=waiverwatch-run   # identity the service runs as: no roles, it only calls public APIs
DEPLOY_SA=github-deploy      # identity GitHub Actions deploys as
POOL=github
PROVIDER=github-oidc
BUDGET_CZK=25                # about $1: any spend at all means something is wrong

gc() { gcloud --project "$PROJECT" --quiet "$@"; }
say() { printf '\n== %s\n' "$*"; }

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
  billingbudgets.googleapis.com

say "Artifact Registry: $AR_REPO in $REGION (keeps the 5 newest images)"
if ! gc artifacts repositories describe "$AR_REPO" --location "$REGION" >/dev/null 2>&1; then
  gc artifacts repositories create "$AR_REPO" --location "$REGION" --repository-format docker \
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
for sa in "$RUNTIME_SA:waiverwatch runtime (no roles)" "$DEPLOY_SA:GitHub Actions deploys"; do
  name=${sa%%:*}
  if ! gc iam service-accounts describe "$name@$PROJECT.iam.gserviceaccount.com" >/dev/null 2>&1; then
    gc iam service-accounts create "$name" --display-name "${sa#*:}"
  fi
done

say "Deployer permissions"
# run.admin (not run.developer) because making the service public needs setIamPolicy.
gc projects add-iam-policy-binding "$PROJECT" --member "serviceAccount:$DEPLOY_EMAIL" \
  --role roles/run.admin --condition None >/dev/null
gc artifacts repositories add-iam-policy-binding "$AR_REPO" --location "$REGION" \
  --member "serviceAccount:$DEPLOY_EMAIL" --role roles/artifactregistry.writer >/dev/null
# Deploying a service that runs as RUNTIME_SA requires acting as it.
gc iam service-accounts add-iam-policy-binding "$RUNTIME_EMAIL" \
  --member "serviceAccount:$DEPLOY_EMAIL" --role roles/iam.serviceAccountUser >/dev/null

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
gc iam service-accounts add-iam-policy-binding "$DEPLOY_EMAIL" \
  --role roles/iam.workloadIdentityUser \
  --member "principalSet://iam.googleapis.com/$POOL_ID/attribute.repository/$REPO" >/dev/null

say "Budget alert: $BUDGET_CZK CZK/month on $PROJECT"
if ! gcloud billing budgets list --billing-account "$BILLING" --format='value(displayName)' | grep -qx waiverwatch; then
  gcloud billing budgets create --billing-account "$BILLING" --display-name waiverwatch \
    --budget-amount "${BUDGET_CZK}CZK" --filter-projects "projects/$PROJECT" \
    --threshold-rule percent=0.5 --threshold-rule percent=1.0
fi

say "GitHub repository variables"
gh variable set GCP_PROJECT_ID --repo "$REPO" --body "$PROJECT"
gh variable set GCP_REGION --repo "$REPO" --body "$REGION"
gh variable set GCP_WIF_PROVIDER --repo "$REPO" --body "$POOL_ID/providers/$PROVIDER"
gh variable set GCP_DEPLOY_SA --repo "$REPO" --body "$DEPLOY_EMAIL"
gh variable set GCP_RUNTIME_SA --repo "$REPO" --body "$RUNTIME_EMAIL"
gh variable set GCP_IMAGE --repo "$REPO" --body "$REGION-docker.pkg.dev/$PROJECT/$AR_REPO/waiverwatch"

say "Done"
