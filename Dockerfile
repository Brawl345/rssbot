FROM golang:1.22 AS build-stage
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
RUN CGO_ENABLED=0 GOOS=linux go build -o /rssbot

FROM gcr.io/distroless/static-debian12 AS release-stage
WORKDIR /app
COPY --from=build-stage /rssbot /app/rssbot
USER nonroot:nonroot
ENTRYPOINT ["/app/rssbot"]
