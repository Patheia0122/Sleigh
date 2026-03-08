FROM golang:1.26 AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/server ./cmd/server

FROM alpine:3.20

WORKDIR /app
COPY --from=builder /out/server /app/server

RUN apk add --no-cache docker-cli ca-certificates

EXPOSE 8080

ENTRYPOINT ["/app/server"]
