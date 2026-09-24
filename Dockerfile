FROM golang:1.27.1@sha256:3680233e3204827fbdc66088528ae6d4b3d034f51d03a99d454f6de034888244 AS build-stage
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags "-X github.com/Brawl345/rssbot/fetcher.Version=${VERSION}" -o /rssbot

FROM gcr.io/distroless/static-debian13:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3 AS release-stage
WORKDIR /app
COPY --from=build-stage /rssbot /app/rssbot
USER nonroot:nonroot
ENTRYPOINT ["/app/rssbot"]
