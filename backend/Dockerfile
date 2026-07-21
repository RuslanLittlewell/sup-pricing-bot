FROM golang:1.26-alpine AS builder

RUN apk add --no-cache git ca-certificates chromium nss freetype harfbuzz

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

# Pin the diagnostic extension so production builds are reproducible. Scrapfly owns
# this repository and has explicitly authorised its use in this test deployment.
RUN git clone https://github.com/scrapfly/Antibot-Detector.git /opt/antibot-detector \
    && cd /opt/antibot-detector \
    && git checkout e5341e9ff4e3fdd5302a93980d74b9923686e987 \
    && rm -rf .git

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o /app/api ./cmd/api
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/worker ./cmd/worker

FROM alpine:3.23

RUN apk add --no-cache \
    ca-certificates \
    tzdata \
    chromium \
    xvfb \
    nss \
    freetype \
    harfbuzz \
    && rm -rf /var/cache/apk/*

ENV CHROME_PATH=/usr/bin/chromium

COPY --from=builder /opt/antibot-detector /opt/extensions/antibot-detector
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh

COPY --from=builder /app/api /usr/local/bin/api
COPY --from=builder /app/worker /usr/local/bin/worker

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["api"]
