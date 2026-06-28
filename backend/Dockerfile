FROM golang:1.26-alpine AS builder

RUN apk add --no-cache git ca-certificates chromium nss freetype harfbuzz

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o /app/api ./cmd/api
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/worker ./cmd/worker

FROM alpine:3.19

RUN apk add --no-cache \
    ca-certificates \
    tzdata \
    chromium \
    nss \
    freetype \
    harfbuzz \
    && rm -rf /var/cache/apk/*

ENV CHROME_PATH=/usr/bin/chromium

COPY --from=builder /app/api /usr/local/bin/api
COPY --from=builder /app/worker /usr/local/bin/worker

EXPOSE 8080

CMD ["api"]
