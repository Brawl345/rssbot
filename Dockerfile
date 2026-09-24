FROM golang:1.22 AS build-stage
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags "-X github.com/Brawl345/rssbot/fetcher.Version=${VERSION}" -o /rssbot

FROM gcr.io/distroless/static-debian12 AS release-stage
WORKDIR /app
COPY --from=build-stage /rssbot /app/rssbot
USER nonroot:nonroot
ENTRYPOINT ["/app/rssbot"]
