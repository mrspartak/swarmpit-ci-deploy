# Simple [Swarmpit](https://github.com/swarmpit/swarmpit) API tool to redeploy services by name
I made this app because Swarmpit can't update services from private repositories, so I can use it in CI "deploy" stage via curl.
Since v1 it also **watches the rollout** and tells you when a deploy fails: rollback, paused update, crash loop or timeout.

Single static Go binary, no dependencies, `FROM scratch` image (a few MB).

[![CI](https://img.shields.io/github/actions/workflow/status/mrspartak/swarmpit-ci-deploy/ci.yml?branch=master&style=for-the-badge&label=CI)](https://github.com/mrspartak/swarmpit-ci-deploy/actions/workflows/ci.yml)
[![Docker Pulls](https://img.shields.io/docker/pulls/assorium/swarmpit-ci-deploy?style=for-the-badge "Docker Pulls")](https://hub.docker.com/r/assorium/swarmpit-ci-deploy "Docker Pulls")
[![Latest Github tag](https://img.shields.io/github/v/tag/mrspartak/swarmpit-ci-deploy?sort=date&style=for-the-badge "Latest Github tag")](https://github.com/mrspartak/swarmpit-ci-deploy/releases "Latest Github tag")

## Environment variables
| Variable | Default | Description |
| --- | --- | --- |
| `APP_PORT` | `3052` | port to listen on |
| `APP_KEY` | | `key` query value that protects the endpoint. Can also be read from a file via `APP_KEY_CONFIG` (path) or `APP_KEY_SECRET` (name under `/run/secrets`) |
| `SWARMPIT_URL` | `http://127.0.0.1:888` | Swarmpit base URL |
| `SWARMPIT_AUTH` | | Bearer token from Swarmpit > Profile Settings > API Access, e.g. `Bearer eyJ...`. Can also come from `SWARMPIT_AUTH_CONFIG` / `SWARMPIT_AUTH_SECRET` |
| `ALERT_WEBHOOK` | | GET URL with a `{MESSAGE}` placeholder, called on every deploy result and on errors. The message is url-encoded for you |
| `WAIT_DEFAULT` | | set to `1` to make `/redeploy` block until the rollout converges unless the call passes `wait=0` |
| `WATCH_TIMEOUT` | `300` | seconds to wait for a rollout to converge before reporting failure |
| `WATCH_SETTLE` | `30` | seconds all replicas must stay running after the update completes (catches crash loops) |
| `WATCH_INTERVAL` | `3` | seconds between Swarmpit polls |
| `DEBUG` | | set to anything to log every request |

Example webhook for Telegram:
`ALERT_WEBHOOK=https://api.telegram.org/bot<token>/sendMessage?chat_id=<chat>&text={MESSAGE}`

## Docker
```
docker config create swarmpit_token "Bearer eyJ..."

docker service create --name swarmpit-ci-deploy -p 3052:3052 \
  -e APP_KEY=123 -e SWARMPIT_URL=http://swarmpit:8080 \
  -e SWARMPIT_AUTH_CONFIG=/swarmpit_token \
  --config src=swarmpit_token,target=/swarmpit_token \
  assorium/swarmpit-ci-deploy:latest
```
The image has a `GET /health` endpoint (204) for your own healthchecks.

## Usage
```
GET /redeploy
  query:
    key:     APP_KEY
    name:    service name            (or)
    id:      service id, comma separated
    wait:    1 to block until the rollout converges (recommended for CI), 0 to return at once; default from WAIT_DEFAULT
    timeout: override WATCH_TIMEOUT in seconds for this call

RETURNS JSON {success: Boolean, error?: String}
```

Every redeploy is watched, with or without `wait`:

| Outcome | Detected by | Result |
| --- | --- | --- |
| success | Docker update state `completed` and all replicas running for `WATCH_SETTLE` | webhook `DEPLOY > SUCCESS > #name`, HTTP 200 |
| rollback / paused | update state `rollback_*` or `paused` | webhook `DEPLOY > FAILED > #name > update rollback_completed: ... \| tasks: failed: task: non-zero exit (1)`, HTTP 500 |
| crash loop / stuck | replicas never stay up before `WATCH_TIMEOUT` | webhook `DEPLOY > FAILED > #name > timeout after 5m0s: ...`, HTTP 500 |

Without `wait` (and without `WAIT_DEFAULT`) the request returns `202` immediately and only the webhook reports the outcome.
For rollback detection to work, give your services an update policy, e.g. in the stack file:
```yml
deploy:
  update_config:
    failure_action: rollback
    monitor: 30s
```
and a `HEALTHCHECK` in the image, otherwise Docker considers any started container a success.

### GitLab CI
`curl -f` makes the job fail when the deploy fails.
```yml
deploy:
  stage: deploy
  image: curlimages/curl
  script:
    - curl -fsS --max-time 600 "${DEPLOY_URL}/redeploy?key=${DEPLOY_KEY}&name=${DEPLOY_NAME}&wait=1"
  only:
    - master
```
DEPLOY_URL - URL of this service, for example http://123.123.123.123:3052
DEPLOY_KEY - the APP_KEY value
DEPLOY_NAME - name of the service to update

## Development
```
go test -race ./...
golangci-lint run ./...
docker build --build-arg VERSION=dev -t assorium/swarmpit-ci-deploy:latest .
```

## Releasing
CI lints, tests and builds the image on every push and pull request.
Pushing a `vX.Y.Z` tag builds a multi-arch image and publishes it to Docker Hub as `assorium/swarmpit-ci-deploy:X.Y.Z` and `:latest`.
The release workflow needs the `DOCKERHUB_USERNAME` and `DOCKERHUB_TOKEN` repository secrets.
