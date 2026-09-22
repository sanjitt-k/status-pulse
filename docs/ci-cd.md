# CI and image releases

The `CI` GitHub Actions workflow runs on branch pushes, pull requests, version-tag
pushes, and manual dispatch. Commit and push the workflow to activate it on GitHub.

## Pipeline

1. **Go checks**: formatting, dependency verification, vet, all tests with the race
   detector, and a CGo-free binary build.
2. **Container checks**: Compose validation, Docker build, and a container smoke
   test covering health, API response, non-root UID, certificates, and clean stop.
3. **Publish versioned image**: pushed `v*` tags reach this job after both checks
   succeed. It requires `vMAJOR.MINOR.PATCH`, builds and smoke-tests the release image,
   then publishes version and full-commit-SHA tags to GHCR.

Ordinary pushes, PRs (including fork PRs), and manual dispatch never log in or
publish. The workflow uses `pull_request`, not `pull_request_target`. Checkout
does not retain credentials. Only the gated publish job grants `packages: write`;
all other jobs receive `contents: read`. Registry login uses the short-lived
repository `GITHUB_TOKEN`; no personal token or Docker Hub secret is needed.

Go is selected from `.go-version`. Docker builds in CI pass the same version as
a build argument. Keep the Dockerfile's default `GO_VERSION` aligned when changing
the version. The `go.mod` directive remains the minimum supported language version.
Actions are pinned to verified commit hashes; update pins deliberately.

## Release procedure

After merging and checking a green CI run, tag the intended commit:

```sh
git tag v0.1.0
git push origin v0.1.0
```

The tagged commit must contain the workflow. Watch the CI run in GitHub Actions.
An invalid `v*` tag fails validation without publishing. Avoid moving released
tags; use a new patch version for changes.

For this repository, a successful release publishes:

```text
ghcr.io/sanjitt-k/status-pulse:v0.1.0
ghcr.io/sanjitt-k/status-pulse:sha-<full-commit-SHA>
```

The image name is derived from the repository, so forks publish to their own
namespace. OCI image labels record source, version, and commit. No floating
`latest` tag is generated. For immutable deployment references, use the image
digest reported by `docker push`; tags can be overwritten by later pushes.

GitHub Actions and package publishing must be enabled by repository/organization
policy. A new GHCR package may initially be private; public pulls require setting
its visibility appropriately, or authenticating for private pulls. An existing
package may require explicitly granting this repository Actions access.

If publishing fails, inspect the failed job and rerun after fixing permissions.
The two tag pushes are separate operations: the version tag can exist even if
the SHA-tag push fails. Re-running the workflow can complete both pushes.

## Local checks and failure behavior

```sh
go test -race -count=1 -timeout=120s ./...
go vet ./...
docker build --build-arg "GO_VERSION=$(cat .go-version)" -t statuspulse:ci .
bash scripts/container-smoke.sh statuspulse:ci
```

The smoke script requires Bash and Docker; on Windows use Git Bash. It creates
an isolated container and anonymous data volume, then removes both, including
on failure. It does not touch the normal Compose data volume.

A nonzero formatting, vet, test, or build result fails the job. Failed Go checks
skip container checks and publication; a failed container check skips publication.
For a demonstration, add a deliberately failing test on a disposable branch,
push it, observe the red Go checks job, then remove that test. Do not release
that branch. Consider requiring `Go checks` and `Container checks` in branch
protection; those repository settings are not changed by this implementation.

This phase delivers tested images. Deployment automation and Kubernetes are
separate later phases; no cluster or production deployment is configured here.
