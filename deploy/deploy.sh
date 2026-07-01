#!/usr/bin/env bash
set -euo pipefail

VPS_HOST="${VPS_HOST:-167.233.159.2}"
VPS_USER="${VPS_USER:-root}"
DEPLOY_DIR="${DEPLOY_DIR:-/opt/price-checker-bot}"

# Use real DNS names that point to VPS_HOST. Examples:
#   APP_DOMAIN=pricebot.example.com
#   API_DOMAIN=api.pricebot.example.com
APP_DOMAIN="${APP_DOMAIN:-pricebot.littlewell-app.work}"
API_DOMAIN="${API_DOMAIN:-pricebot-api.littlewell-app.work}"
CERTBOT_EMAIL="${CERTBOT_EMAIL:-admin@${APP_DOMAIN}}"
ENABLE_CERTBOT="${ENABLE_CERTBOT:-0}"
FRONTEND_HOST_PORT="${FRONTEND_HOST_PORT:-13000}"
BACKEND_HOST_PORT="${BACKEND_HOST_PORT:-18080}"

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REMOTE="${VPS_USER}@${VPS_HOST}"

dotenv_get() {
  local key="$1"
  local file="${PROJECT_ROOT}/.env"
  [[ -f "$file" ]] || return 0
  grep -E "^${key}=" "$file" | tail -n 1 | cut -d= -f2- || true
}

TELEGRAM_BOT_TOKEN="${TELEGRAM_BOT_TOKEN:-$(dotenv_get TELEGRAM_BOT_TOKEN)}"
BOT_USERNAME="${BOT_USERNAME:-$(dotenv_get BOT_USERNAME)}"
BOT_USERNAME="${BOT_USERNAME:-sur_price_bot}"
SCRAPER_COOKIES_FILE="${SCRAPER_COOKIES_FILE:-$(dotenv_get SCRAPER_COOKIES_FILE)}"
SCRAPER_PROXY_URL="${SCRAPER_PROXY_URL:-$(dotenv_get SCRAPER_PROXY_URL)}"

if [[ -z "${TELEGRAM_BOT_TOKEN}" ]]; then
  echo "TELEGRAM_BOT_TOKEN is required. Export it or add it to .env." >&2
  exit 1
fi

if ! command -v docker >/dev/null 2>&1; then
  echo "Docker is required locally only for optional checks, but was not found. Continuing." >&2
fi

SSH_BASE=(ssh -o StrictHostKeyChecking=accept-new)
if [[ -n "${SSH_PASSWORD:-}" ]]; then
  if ! command -v sshpass >/dev/null 2>&1; then
    echo "SSH_PASSWORD is set, but sshpass is not installed. Install sshpass or use SSH key/password prompt." >&2
    exit 1
  fi
  SSH_BASE=(sshpass -p "${SSH_PASSWORD}" ssh -o StrictHostKeyChecking=accept-new)
fi

run_remote() {
  "${SSH_BASE[@]}" "$REMOTE" "$@"
}

echo "Deploy target: ${REMOTE}:${DEPLOY_DIR}"
echo "Frontend: https://${APP_DOMAIN}"
echo "API/admin: https://${API_DOMAIN}"

run_remote "mkdir -p '${DEPLOY_DIR}'"

echo "Uploading source..."
tar \
  --exclude='.git' \
  --exclude='.env' \
  --exclude='.env.*' \
  --exclude='frontend/node_modules' \
  --exclude='frontend/dist' \
  --exclude='backend/tmp' \
  --exclude='*.log' \
  -C "$PROJECT_ROOT" \
  -czf - . | run_remote "tar -xzf - -C '${DEPLOY_DIR}'"

echo "Preparing scraper cookies on server..."
run_remote "mkdir -p '${DEPLOY_DIR}/deploy/secrets' && chmod 700 '${DEPLOY_DIR}/deploy/secrets'"
if [[ -n "${SCRAPER_COOKIES_FILE}" && -f "${SCRAPER_COOKIES_FILE}" ]]; then
  tar -C "$(dirname "${SCRAPER_COOKIES_FILE}")" -czf - "$(basename "${SCRAPER_COOKIES_FILE}")" | \
    run_remote "tar -xzf - -C '${DEPLOY_DIR}/deploy/secrets' && mv '${DEPLOY_DIR}/deploy/secrets/$(basename "${SCRAPER_COOKIES_FILE}")' '${DEPLOY_DIR}/deploy/secrets/scraper-cookies.json' && chmod 600 '${DEPLOY_DIR}/deploy/secrets/scraper-cookies.json'"
else
  run_remote "if [[ ! -f '${DEPLOY_DIR}/deploy/secrets/scraper-cookies.json' ]]; then printf '%s\n' '{\"cookies\":[]}' > '${DEPLOY_DIR}/deploy/secrets/scraper-cookies.json'; chmod 600 '${DEPLOY_DIR}/deploy/secrets/scraper-cookies.json'; fi"
fi

echo "Preparing production env on server..."
run_remote "cd '${DEPLOY_DIR}' && \
  touch .env.production && chmod 600 .env.production && \
  if ! grep -q '^POSTGRES_PASSWORD=' .env.production; then echo POSTGRES_PASSWORD=\$(openssl rand -hex 24) >> .env.production; fi && \
  if ! grep -q '^BETTER_AUTH_SECRET=' .env.production; then echo BETTER_AUTH_SECRET=\$(openssl rand -hex 32) >> .env.production; fi && \
  if ! grep -q '^ADMIN_TOKEN=' .env.production; then echo ADMIN_TOKEN=\$(openssl rand -hex 32) >> .env.production; fi && \
  grep -v -E '^(APP_DOMAIN|API_DOMAIN|FRONTEND_HOST_PORT|BACKEND_HOST_PORT|TELEGRAM_BOT_TOKEN|BOT_USERNAME|SCRAPER_PROXY_URL)=' .env.production > .env.production.tmp && mv .env.production.tmp .env.production && \
  printf '%s\n' \
    'APP_DOMAIN=${APP_DOMAIN}' \
    'API_DOMAIN=${API_DOMAIN}' \
    'FRONTEND_HOST_PORT=${FRONTEND_HOST_PORT}' \
    'BACKEND_HOST_PORT=${BACKEND_HOST_PORT}' \
    'TELEGRAM_BOT_TOKEN=${TELEGRAM_BOT_TOKEN}' \
    'BOT_USERNAME=${BOT_USERNAME}' \
    'SCRAPER_PROXY_URL=${SCRAPER_PROXY_URL}' >> .env.production"

echo "Installing Docker on server if needed..."
run_remote "if ! command -v docker >/dev/null 2>&1; then curl -fsSL https://get.docker.com | sh; fi"

echo "Docker/Compose on server:"
run_remote "docker --version || true; docker compose version || true; docker-compose version || true; docker-compose-v2 version || true"

echo "Installing Docker Compose on server if needed..."
run_remote "if ! docker compose version >/dev/null 2>&1 && ! command -v docker-compose >/dev/null 2>&1 && ! command -v docker-compose-v2 >/dev/null 2>&1; then \
  apt-get update && \
  (apt-get install -y docker-compose-plugin || apt-get install -y docker-compose-v2 || apt-get install -y docker-compose); \
fi"

echo "Starting containers..."
run_remote "cd '${DEPLOY_DIR}' && set -a && . ./.env.production && set +a && export COMPOSE_FILE=deploy/docker-compose.prod.yml && if docker compose version >/dev/null 2>&1; then compose_cmd='docker compose'; elif command -v docker-compose >/dev/null 2>&1; then compose_cmd='docker-compose'; elif command -v docker-compose-v2 >/dev/null 2>&1; then compose_cmd='docker-compose-v2'; else echo 'Docker Compose is not installed. Install docker-compose-plugin, docker-compose-v2, or docker-compose.' >&2; exit 1; fi; \$compose_cmd up -d --build"

echo "Container status:"
run_remote "cd '${DEPLOY_DIR}' && set -a && . ./.env.production && set +a && export COMPOSE_FILE=deploy/docker-compose.prod.yml && if docker compose version >/dev/null 2>&1; then compose_cmd='docker compose'; elif command -v docker-compose >/dev/null 2>&1; then compose_cmd='docker-compose'; elif command -v docker-compose-v2 >/dev/null 2>&1; then compose_cmd='docker-compose-v2'; else echo 'Docker Compose is not installed. Install docker-compose-plugin, docker-compose-v2, or docker-compose.' >&2; exit 1; fi; \$compose_cmd ps"

echo "Configuring nginx reverse proxy..."
run_remote "if ! command -v nginx >/dev/null 2>&1; then apt-get update && apt-get install -y nginx; fi
cat > /etc/nginx/sites-available/price-checker-bot.conf <<'NGINX'
server {
    listen 80;
    listen [::]:80;
    server_name ${APP_DOMAIN};

    location / {
        proxy_pass http://127.0.0.1:${FRONTEND_HOST_PORT};
        proxy_http_version 1.1;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection \"upgrade\";
    }
}

server {
    listen 80;
    listen [::]:80;
    server_name ${API_DOMAIN};

    location / {
        proxy_pass http://127.0.0.1:${BACKEND_HOST_PORT};
        proxy_http_version 1.1;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
    }
}
NGINX
ln -sf /etc/nginx/sites-available/price-checker-bot.conf /etc/nginx/sites-enabled/price-checker-bot.conf
rm -f /etc/nginx/sites-enabled/default
sed -i '/return 301 https:\/\/\$host\$request_uri;/d' /etc/nginx/sites-available/price-checker-bot.conf
sed -i '/if (\$host = ${APP_DOMAIN})/,+2d' /etc/nginx/sites-available/price-checker-bot.conf
sed -i '/if (\$host = ${API_DOMAIN})/,+2d' /etc/nginx/sites-available/price-checker-bot.conf
nginx -t
systemctl reload nginx"

if [[ "${ENABLE_CERTBOT}" = "1" ]]; then
  echo "Configuring HTTPS certificates with certbot..."
  run_remote "if ! command -v certbot >/dev/null 2>&1; then apt-get update && apt-get install -y certbot python3-certbot-nginx; fi
certbot --nginx \
  --non-interactive \
  --agree-tos \
  --email '${CERTBOT_EMAIL}' \
  -d '${APP_DOMAIN}' \
  -d '${API_DOMAIN}'
nginx -t
systemctl reload nginx"
else
  echo "Skipping certbot because ENABLE_CERTBOT=${ENABLE_CERTBOT}."
fi

echo "Done."
