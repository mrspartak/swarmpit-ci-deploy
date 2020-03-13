# Simple Swarmpit API tool to redeploy services by name
I made this app because Swarmpit can't update services from private repositories, so I can use it in CI "deploy" stage via curl.

[![Docker Cloud Automated build](https://img.shields.io/docker/cloud/automated/assorium/swarmpit-ci-deploy?style=for-the-badge "Docker Cloud Automated build")](https://hub.docker.com/r/assorium/swarmpit-ci-deploy "Docker Cloud Automated build")
[![Docker Cloud Build Status](https://img.shields.io/docker/cloud/build/assorium/swarmpit-ci-deploy?style=for-the-badge "Docker Cloud Build Status")](https://hub.docker.com/r/assorium/swarmpit-ci-deploy "Docker Cloud Build Status")
[![Docker Pulls](https://img.shields.io/docker/pulls/assorium/swarmpit-ci-deploy?style=for-the-badge "Docker Pulls")](https://hub.docker.com/r/assorium/swarmpit-ci-deploy "Docker Pulls")  <br/>

[![Latest Github tag](https://img.shields.io/github/v/tag/mrspartak/swarmpit-ci-deploy?sort=date&style=for-the-badge "Latest Github tag")](https://github.com/mrspartak/swarmpit-ci-deploy/releases "Latest Github tag")


## Environment variables
    #port app will be launched at
    const APP_PORT = process.env.APP_PORT || 3052
    const APP_KEY = process.env.APP_KEY
    key query to protect direct access
    You can also pass key via APP_KEY_CONFIG or APP_KEY_SECRET (docker config or secret file)

    #Debug
    const DEBUG = process.env.DEBUG || false;

    #Swarmpit settings
    const SWARMPIT_URL = process.env.SWARMPIT_URL || 'http://127.0.0.1:888';

    const SWARMPIT_AUTH = process.env.SWARMPIT_AUTH
    this is Bearer token, it could be obtained in Swampit > Profile Settings > API Access
    You can also pass this token via SWARMPIT_AUTH_CONFIG or SWARMPIT_AUTH_SECRET (docker config or secret file)


## Docker
you can use config or secret file
```
docker config create swarmpit_token "Beared gihrogio..." or you can just add local file with volume

docker run -p 3050:3050 --name swarmpit-ci-deploy \
  -e APP_KEY=123 -e SWARMPIT_AUTH_CONFIG=swarmpit_token \
  --config src=swarmpit_token,target="/home/app/tracker.txt" \
  assorium/swarmpit-ci-deploy:latest
```

## Usage
Can redirect such links:
```
//Redeploy
GET /redeploy
  query:
    name: serviceName 
    id: id of service

```