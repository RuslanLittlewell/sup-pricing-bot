#!/usr/bin/env bash
set -euo pipefail

VPS_HOST="${VPS_HOST:-87.120.196.46}"
VPS_USER="${VPS_USER:-root}"
DEPLOY_DIR="${DEPLOY_DIR:-/opt/price-checker-bot}"

# Use real DNS names that point to VPS_HOST. Examples:
#   APP_DOMAIN=pricebot.example.com
#   API_DOMAIN=api.pricebot.example.com
APP_DOMAIN="${APP_DOMAIN:-surpricebot.com}"
API_DOMAIN="${API_DOMAIN:-pricebot-api.surpricebot.com}"
CERTBOT_EMAIL="${CERTBOT_EMAIL:-admin@${APP_DOMAIN}}"
ENABLE_CERTBOT="${ENABLE_CERTBOT:-1}"
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
CF_RELAY_URL="${CF_RELAY_URL:-$(dotenv_get CF_RELAY_URL)}"
CF_RELAY_TOKEN="${CF_RELAY_TOKEN:-$(dotenv_get CF_RELAY_TOKEN)}"
OPEN_SERP_ENGINES="${OPEN_SERP_ENGINES:-$(dotenv_get OPEN_SERP_ENGINES)}"
OPEN_SERP_API_KEY="${OPEN_SERP_API_KEY:-$(dotenv_get OPEN_SERP_API_KEY)}"
SERPER_API_KEY="${SERPER_API_KEY:-$(dotenv_get SERPER_API_KEY)}"
SERPAPI_KEY="${SERPAPI_KEY:-$(dotenv_get SERPAPI_KEY)}"
SERPAPI_KEY_2="${SERPAPI_KEY_2:-$(dotenv_get SERPAPI_KEY_2)}"
GEMINI_API_KEY="${GEMINI_API_KEY:-$(dotenv_get GEMINI_API_KEY)}"
GEMINI_MODEL="${GEMINI_MODEL:-$(dotenv_get GEMINI_MODEL)}"
ADMIN_USERNAME="${ADMIN_USERNAME:-$(dotenv_get ADMIN_USERNAME)}"
ADMIN_PASSWORD="${ADMIN_PASSWORD:-$(dotenv_get ADMIN_PASSWORD)}"

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

# Clean previously-uploaded source before extracting the new tar. `tar -x` overwrites
# files but never deletes ones that no longer exist in the archive, so without this
# step files removed between deploys (e.g. deleted pages/modules) linger on the server
# and break the Docker build (astro/go still compile the stale sources). Preserve only
# server-generated state that lives in DEPLOY_DIR: .env.production and deploy/secrets.
echo "Cleaning stale source on server..."
run_remote "cd '${DEPLOY_DIR}' && \
  find . -mindepth 1 -maxdepth 1 ! -name '.env.production' ! -name 'deploy' -exec rm -rf {} + && \
  if [ -d deploy ]; then find deploy -mindepth 1 -maxdepth 1 ! -name 'secrets' -exec rm -rf {} +; fi"

echo "Uploading source..."
tar \
  --no-xattrs \
  --exclude='.git' \
  --exclude='.env' \
  --exclude='.env.*' \
  --exclude='frontend/node_modules' \
  --exclude='frontend/dist' \
	--exclude='admin/node_modules' \
	--exclude='admin/dist' \
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
  grep -v -E '^(APP_DOMAIN|API_DOMAIN|FRONTEND_HOST_PORT|BACKEND_HOST_PORT|TELEGRAM_BOT_TOKEN|BOT_USERNAME|SCRAPER_PROXY_URL|CF_RELAY_URL|CF_RELAY_TOKEN|OPEN_SERP_ENGINES|OPEN_SERP_API_KEY|SERPER_API_KEY|SERPAPI_KEY|SERPAPI_KEY_2|GEMINI_API_KEY|GEMINI_MODEL|ADMIN_USERNAME|ADMIN_PASSWORD)=' .env.production > .env.production.tmp && mv .env.production.tmp .env.production && \
  printf '%s\n' \
    'APP_DOMAIN=${APP_DOMAIN}' \
    'API_DOMAIN=${API_DOMAIN}' \
    'FRONTEND_HOST_PORT=${FRONTEND_HOST_PORT}' \
    'BACKEND_HOST_PORT=${BACKEND_HOST_PORT}' \
    'TELEGRAM_BOT_TOKEN=${TELEGRAM_BOT_TOKEN}' \
    'BOT_USERNAME=${BOT_USERNAME}' \
    'SCRAPER_PROXY_URL=${SCRAPER_PROXY_URL}' \
    'CF_RELAY_URL=${CF_RELAY_URL}' \
    'CF_RELAY_TOKEN=${CF_RELAY_TOKEN}' \
    'OPEN_SERP_ENGINES=${OPEN_SERP_ENGINES}' \
    'OPEN_SERP_API_KEY=${OPEN_SERP_API_KEY}' \
    'SERPER_API_KEY=${SERPER_API_KEY}' \
    'SERPAPI_KEY=${SERPAPI_KEY}' \
    'SERPAPI_KEY_2=${SERPAPI_KEY_2}' \
    'GEMINI_API_KEY=${GEMINI_API_KEY}' \
    'GEMINI_MODEL=${GEMINI_MODEL}' \
    'ADMIN_USERNAME=${ADMIN_USERNAME}' \
    'ADMIN_PASSWORD=${ADMIN_PASSWORD}' >> .env.production"

echo "Installing Docker on server if needed..."
run_remote "if ! command -v docker >/dev/null 2>&1; then curl -fsSL https://get.docker.com | sh; fi"

echo "Docker/Compose on server:"
run_remote "docker --version || true; docker compose version || true; docker-compose version || true; docker-compose-v2 version || true"

echo "Installing Docker Compose on server if needed..."
run_remote "if ! docker compose version >/dev/null 2>&1 && ! command -v docker-compose >/dev/null 2>&1 && ! command -v docker-compose-v2 >/dev/null 2>&1; then \
  apt-get update && \
  (apt-get install -y docker-compose-plugin || apt-get install -y docker-compose-v2 || apt-get install -y docker-compose); \
fi"

echo "Building services sequentially, then starting containers..."
run_remote "cd '${DEPLOY_DIR}' && set -a && . ./.env.production && set +a && export COMPOSE_FILE=deploy/docker-compose.prod.yml && if docker compose version >/dev/null 2>&1; then compose_cmd='docker compose'; elif command -v docker-compose >/dev/null 2>&1; then compose_cmd='docker-compose'; elif command -v docker-compose-v2 >/dev/null 2>&1; then compose_cmd='docker-compose-v2'; else echo 'Docker Compose is not installed. Install docker-compose-plugin, docker-compose-v2, or docker-compose.' >&2; exit 1; fi; for service in curl-cffi firefox-renderer backend worker frontend; do \$compose_cmd build \$service || exit 1; done; \$compose_cmd up -d --no-build"

echo "Container status:"
run_remote "cd '${DEPLOY_DIR}' && set -a && . ./.env.production && set +a && export COMPOSE_FILE=deploy/docker-compose.prod.yml && if docker compose version >/dev/null 2>&1; then compose_cmd='docker compose'; elif command -v docker-compose >/dev/null 2>&1; then compose_cmd='docker-compose'; elif command -v docker-compose-v2 >/dev/null 2>&1; then compose_cmd='docker-compose-v2'; else echo 'Docker Compose is not installed. Install docker-compose-plugin, docker-compose-v2, or docker-compose.' >&2; exit 1; fi; \$compose_cmd ps"

# nginx_site_conf prints the full site config. With SSL (cert already issued) it
# includes the 443 servers directly, so a deploy never passes through an HTTP-only
# intermediate state — the old flow rewrote the config without any `listen 443` and
# relied on certbot to re-add it, leaving HTTPS down for the window in between (or
# until the next deploy, if the script died before the certbot step). Nginx runtime
# variables ($host etc.) are backslash-escaped; everything else expands locally.
nginx_site_conf() {
  local ssl="$1" # "yes" once /etc/letsencrypt/live/${APP_DOMAIN} exists, else "no"
  if [[ "$ssl" == "yes" ]]; then
    cat <<NGINX
server {
    listen 80;
    listen [::]:80;
    server_name ${APP_DOMAIN} ${API_DOMAIN};
    return 301 https://\$host\$request_uri;
}

server {
    listen 443 ssl;
    listen [::]:443 ssl;
    server_name ${APP_DOMAIN};
    ssl_certificate /etc/letsencrypt/live/${APP_DOMAIN}/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/${APP_DOMAIN}/privkey.pem;
    include /etc/letsencrypt/options-ssl-nginx.conf;
    ssl_dhparam /etc/letsencrypt/ssl-dhparams.pem;

    location / {
        proxy_pass http://127.0.0.1:${FRONTEND_HOST_PORT};
        proxy_http_version 1.1;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection "upgrade";
    }
}

server {
    listen 443 ssl;
    listen [::]:443 ssl;
    server_name ${API_DOMAIN};
    ssl_certificate /etc/letsencrypt/live/${APP_DOMAIN}/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/${APP_DOMAIN}/privkey.pem;
    include /etc/letsencrypt/options-ssl-nginx.conf;
    ssl_dhparam /etc/letsencrypt/ssl-dhparams.pem;

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
  else
    cat <<NGINX
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
        proxy_set_header Connection "upgrade";
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
  fi
}

echo "Configuring nginx reverse proxy..."
run_remote "if ! command -v nginx >/dev/null 2>&1; then apt-get update && apt-get install -y nginx; fi"
CERT_EXISTS=$(run_remote "if [[ -f '/etc/letsencrypt/live/${APP_DOMAIN}/fullchain.pem' ]]; then echo yes; else echo no; fi")
# Install the config only when it actually changed, so routine deploys don't touch
# a working nginx at all.
nginx_site_conf "$CERT_EXISTS" | run_remote "cat > /tmp/price-checker-bot.conf.new
if cmp -s /tmp/price-checker-bot.conf.new /etc/nginx/sites-available/price-checker-bot.conf; then
  echo 'nginx config unchanged; skipping reload.'
  rm -f /tmp/price-checker-bot.conf.new
else
  mv /tmp/price-checker-bot.conf.new /etc/nginx/sites-available/price-checker-bot.conf
  ln -sf /etc/nginx/sites-available/price-checker-bot.conf /etc/nginx/sites-enabled/price-checker-bot.conf
  rm -f /etc/nginx/sites-enabled/default
  nginx -t
  systemctl reload nginx
fi"

if [[ "${ENABLE_CERTBOT}" = "1" && "$CERT_EXISTS" != "yes" ]]; then
  echo "Configuring HTTPS certificates with certbot (first-time issuance)..."
  run_remote "if ! command -v certbot >/dev/null 2>&1; then apt-get update && apt-get install -y certbot python3-certbot-nginx; fi
certbot --nginx \
  --non-interactive \
  --agree-tos \
  --email '${CERTBOT_EMAIL}' \
  -d '${APP_DOMAIN}' \
  -d '${API_DOMAIN}'
nginx -t
systemctl reload nginx"
elif [[ "${ENABLE_CERTBOT}" = "1" ]]; then
  echo "Certificate already issued; certbot's renewal timer keeps it fresh."
else
  echo "Skipping certbot because ENABLE_CERTBOT=${ENABLE_CERTBOT}."
fi

echo "Done."
