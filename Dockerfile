FROM golang:1.25-alpine AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/port-tracker ./cmd/trackway

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app

COPY --from=builder /out/port-tracker /app/port-tracker

ENV CONFIG_PATH=/app/config.yaml
ENV LOG_DIR=/data/logs

ENTRYPOINT ["/app/port-tracker"]
