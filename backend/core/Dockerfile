# Multi-stage production build for Railway Revamped Core
FROM golang:1.24-alpine AS builder

WORKDIR /app
COPY go.mod go.sum* ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /app/railway-revamped ./cmd/server

FROM alpine:3.19
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app

COPY --from=builder /app/railway-revamped /app/railway-revamped
COPY ../../frontend /app/frontend

ENV PORT=8080
EXPOSE 8080

ENTRYPOINT ["/app/railway-revamped"]
