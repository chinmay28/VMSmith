#!/usr/bin/env bash
# setup-branch-protection.sh — apply the roadmap 1.1.6 branch-protection
# rules to the repository's main branch via the GitHub API.
#
# Branch protection is a GitHub repository *setting*, not something a code
# change can enable — so this script is the executable form of the rule
# set, runnable by any repo admin:
#
#   - require the CI status checks to pass before merging
#   - require branches to be up to date before merging
#   - block force pushes and deletions
#   - apply the rules to administrators too
#
# Requirements: the `gh` CLI authenticated as a repo admin
# (https://cli.github.com), or set GITHUB_TOKEN with repo admin scope.
#
# Usage:
#   scripts/setup-branch-protection.sh [owner/repo] [branch]
#   (defaults: repo detected from the git remote; branch "main")

set -euo pipefail

REPO="${1:-}"
BRANCH="${2:-main}"

if ! command -v gh >/dev/null 2>&1; then
  echo "error: the gh CLI is required (https://cli.github.com)" >&2
  exit 1
fi

if [ -z "$REPO" ]; then
  REPO=$(gh repo view --json nameWithOwner --jq .nameWithOwner)
fi

# The CI contexts to require. Keep in sync with .github/workflows/ci.yml
# job names.
read -r -d '' PROTECTION_JSON <<'JSON' || true
{
  "required_status_checks": {
    "strict": true,
    "contexts": ["Backend build and tests", "Frontend build and mock GUI tests"]
  },
  "enforce_admins": true,
  "required_pull_request_reviews": null,
  "restrictions": null,
  "allow_force_pushes": false,
  "allow_deletions": false
}
JSON

echo "Applying branch protection to ${REPO}@${BRANCH}..."
echo "$PROTECTION_JSON" | gh api \
  --method PUT \
  -H "Accept: application/vnd.github+json" \
  "repos/${REPO}/branches/${BRANCH}/protection" \
  --input - >/dev/null

echo "Done. Current protection:"
gh api "repos/${REPO}/branches/${BRANCH}/protection" \
  -H "Accept: application/vnd.github+json" \
  --jq '{required_status_checks: .required_status_checks.contexts, strict: .required_status_checks.strict, enforce_admins: .enforce_admins.enabled, allow_force_pushes: .allow_force_pushes.enabled, allow_deletions: .allow_deletions.enabled}'
