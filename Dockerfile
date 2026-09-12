FROM golang:1.27.1-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY *.go ./
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /swarmpit-ci-deploy .

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /swarmpit-ci-deploy /swarmpit-ci-deploy
USER 65534:65534
EXPOSE 3052
ENTRYPOINT ["/swarmpit-ci-deploy"]
