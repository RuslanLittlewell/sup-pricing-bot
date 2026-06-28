# Price Tracker SaaS — MVP Development Plan

## 1. Product Summary

Build a public SaaS for tracking product price changes and stock availability from product pages. Users authenticate only through Telegram, add product URLs, enter the current visible product price, and receive Telegram notifications when the service detects a price change or stock status change.

The first implementation must focus on reliability, not broad promises. The product may accept arbitrary product URLs, but the MVP should be validated primarily on Zara product pages across selected regions.

---

## 2. Confirmed Product Requirements

### 2.1 Target Markets

Initial target markets and regions:

- Poland
- Russia
- Belarus
- Europe
- United States

Important constraint: the service must support different currencies, but must not convert between currencies in MVP.

### 2.2 Initial Test Store

Primary test store for MVP:

- Zara

The service may accept links from other websites, but Zara is the first target for extraction quality testing.

### 2.3 User Input

When creating a tracker, the user provides:

- Product URL
- Current product price visible on the page
- Currency

The entered price is not a target price. It is the current price used for initial verification and extraction calibration.

### 2.4 Notifications

Send Telegram notification when:

- Product price changes
- Product stock status changes
- Product becomes unavailable
- Product becomes available again
- Price extraction breaks and user action is needed

MVP notification rule:

- Notify on any confirmed price change.
- Add anti-spam protection to avoid repeated identical notifications.

### 2.5 Check Frequency

Free plan:

- Check every 60 minutes
- Maximum 5 active tracked products

Future paid plans:

- More tracked products
- More frequent checks
- Longer price history
- Possible priority queue

### 2.6 Authentication

Authentication model:

- Telegram-only login
- Login must be verified because subscriptions are planned later
- No email/password in MVP

Recommended login method:

- Telegram bot deep link login challenge

Example flow:

1. User clicks `Login with Telegram` on the website.
2. Backend creates a temporary login challenge.
3. Frontend opens `https://t.me/<bot>?start=login_<token>`.
4. User presses Start in Telegram.
5. Telegram webhook confirms the token.
6. Backend creates or updates the user.
7. Frontend receives a valid web session.

### 2.7 Languages

Language rollout priority:

1. Russian
2. English
3. Polish

MVP can ship with Russian UI first, but code and route structure should support future i18n.

Recommended locale codes:

- `ru`
- `en`
- `pl`

### 2.8 Website Structure

The public website contains:

- Marketing landing page
- SEO pages
- Pricing page
- Login flow
- User dashboard
- Tracker history

The landing and dashboard may live in the same frontend application.

---

## 3. Critical Product Decision

Do not promise perfect support for every online store.

The correct MVP positioning:

> Track product price changes and stock availability from product pages. Get Telegram alerts when a change is detected.

Avoid saying:

> Works with every store automatically.

Reason: arbitrary web price extraction is unreliable due to dynamic rendering, anti-bot systems, changing HTML, regional prices, cookies, old prices, sale prices, recommended product blocks, and stock variants.

---

## 4. MVP Scope

### 4.1 Must Have

- Telegram-only authentication
- Telegram bot webhook
- User session management
- Add product tracker
- User enters current price and currency
- Price extraction preview
- User confirms detected price candidate
- Stock availability detection
- Free plan limit: 5 active trackers
- Hourly tracker checks
- Price history storage
- Stock history storage
- Telegram notifications on price change
- Telegram notifications on stock status change
- Dashboard with trackers
- Tracker detail page with history
- Basic landing page in Russian
- PostgreSQL database
- Go backend API
- Go worker process
- Basic admin/debug view
- Docker Compose deployment

### 4.2 Should Have

- Pause/resume tracker
- Manual recheck button
- Tracker error status
- Extraction failure notification
- Basic chart for price history
- SEO metadata
- Sitemap
- Robots.txt
- Basic rate limiting
- Basic metrics/logging

### 4.3 Not in MVP

- Payments integration
- Currency conversion
- Browser extension
- Mobile app
- Email notifications
- Perfect support for all stores
- AI-based extraction as primary mechanism
- Kubernetes
- Complex analytics
- Public product pages
- Multi-user teams

---

## 5. Recommended Tech Stack

### 5.1 Backend

- Language: Go
- HTTP router: `chi`
- Database: PostgreSQL
- DB driver: `pgx`
- SQL generation: `sqlc`
- Migrations: `goose`
- Logging: `zerolog` or `zap`
- Config: environment variables
- Worker: separate Go binary

### 5.2 Frontend

Recommended:

- Next.js
- TypeScript
- Server-side rendering for landing and SEO pages
- Dashboard as authenticated web app

Alternative:

- Go templates + HTMX

For this SaaS, Next.js is preferable because the product needs marketing pages, SEO, dashboard, and future pricing pages.

### 5.3 Infrastructure

MVP deployment:

- VPS
- Docker Compose
- Caddy or Nginx reverse proxy
- PostgreSQL
- Go API container
- Go worker container
- Frontend container

Do not use Kubernetes for MVP.

---

## 6. System Architecture

```text
Frontend: Next.js
    |
    | HTTPS
    v
Go API Backend
    |
    +--> PostgreSQL
    |
    +--> Telegram Bot API
    |
    +--> Price Extraction Module
    |
    +--> Admin/Debug Endpoints

Go Worker
    |
    +--> PostgreSQL
    +--> Price Extraction Module
    +--> Telegram Notification Module
```

### 6.1 Repository Structure

```text
price-tracker/
  backend/
    cmd/
      api/
        main.go
      worker/
        main.go
    internal/
      auth/
      billing/
      config/
      db/
      extractor/
      notifier/
      security/
      telegram/
      tracker/
      worker/
    migrations/
    sqlc.yaml
    go.mod
  frontend/
    app/
      [locale]/
        page.tsx
        pricing/
        how-it-works/
        dashboard/
        trackers/
    components/
    lib/
    package.json
  docker-compose.yml
  README.md
```

---

## 7. Database Schema

Use UUID primary keys unless stated otherwise.

### 7.1 `users`

```sql
CREATE TABLE users (
    id UUID PRIMARY KEY,
    telegram_user_id BIGINT UNIQUE NOT NULL,
    telegram_username TEXT,
    telegram_first_name TEXT,
    telegram_last_name TEXT,
    language_code TEXT NOT NULL DEFAULT 'ru',
    plan_code TEXT NOT NULL DEFAULT 'free',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

### 7.2 `login_challenges`

```sql
CREATE TABLE login_challenges (
    id UUID PRIMARY KEY,
    token TEXT UNIQUE NOT NULL,
    telegram_user_id BIGINT,
    status TEXT NOT NULL DEFAULT 'pending',
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

Allowed statuses:

```text
pending
confirmed
consumed
expired
```

### 7.3 `sessions`

```sql
CREATE TABLE sessions (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT UNIQUE NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

### 7.4 `plans`

```sql
CREATE TABLE plans (
    code TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    max_trackers INT NOT NULL,
    check_interval_minutes INT NOT NULL,
    price_history_days INT NOT NULL,
    is_paid BOOLEAN NOT NULL DEFAULT false
);
```

Seed data:

```sql
INSERT INTO plans (
    code,
    name,
    max_trackers,
    check_interval_minutes,
    price_history_days,
    is_paid
) VALUES
('free', 'Free', 5, 60, 30, false),
('pro', 'Pro', 100, 15, 365, true);
```

### 7.5 `trackers`

```sql
CREATE TABLE trackers (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    url TEXT NOT NULL,
    normalized_url TEXT NOT NULL,
    domain TEXT NOT NULL,

    title TEXT,
    image_url TEXT,

    initial_price NUMERIC(14, 2) NOT NULL,
    current_price NUMERIC(14, 2),
    previous_price NUMERIC(14, 2),
    currency TEXT NOT NULL,

    current_stock_status TEXT NOT NULL DEFAULT 'unknown',
    previous_stock_status TEXT,

    extraction_rule JSONB,
    extraction_confidence NUMERIC(5, 2),

    status TEXT NOT NULL DEFAULT 'active',
    last_checked_at TIMESTAMPTZ,
    next_check_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    last_error TEXT,
    consecutive_errors INT NOT NULL DEFAULT 0,

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

Allowed tracker statuses:

```text
active
paused
needs_confirmation
failed
deleted
```

Allowed stock statuses:

```text
in_stock
out_of_stock
unknown
```

### 7.6 `price_points`

```sql
CREATE TABLE price_points (
    id UUID PRIMARY KEY,
    tracker_id UUID NOT NULL REFERENCES trackers(id) ON DELETE CASCADE,
    price NUMERIC(14, 2),
    currency TEXT NOT NULL,
    source TEXT NOT NULL,
    status TEXT NOT NULL,
    error_message TEXT,
    checked_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

Allowed sources:

```text
initial_user_input
auto_extracted
confirmed_by_user
worker_check
```

Allowed statuses:

```text
success
failed
needs_confirmation
```

### 7.7 `stock_points`

```sql
CREATE TABLE stock_points (
    id UUID PRIMARY KEY,
    tracker_id UUID NOT NULL REFERENCES trackers(id) ON DELETE CASCADE,
    stock_status TEXT NOT NULL,
    source TEXT NOT NULL,
    status TEXT NOT NULL,
    error_message TEXT,
    checked_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

### 7.8 `notifications`

```sql
CREATE TABLE notifications (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tracker_id UUID NOT NULL REFERENCES trackers(id) ON DELETE CASCADE,

    type TEXT NOT NULL,

    old_price NUMERIC(14, 2),
    new_price NUMERIC(14, 2),
    currency TEXT,

    old_stock_status TEXT,
    new_stock_status TEXT,

    telegram_message_id BIGINT,
    status TEXT NOT NULL DEFAULT 'pending',
    error_message TEXT,

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at TIMESTAMPTZ
);
```

Allowed notification types:

```text
price_changed
stock_changed
back_in_stock
out_of_stock
extraction_failed
```

Allowed statuses:

```text
pending
sent
failed
skipped
```

---

## 8. Backend API

### 8.1 Auth API

```text
POST /api/auth/telegram/challenge
GET  /api/auth/telegram/challenge/:token
POST /api/auth/telegram/consume
POST /api/auth/logout
GET  /api/me
```

### 8.2 Telegram Webhook

```text
POST /api/telegram/webhook
```

Webhook must handle:

- `/start`
- `/start login_<token>`
- `/help`
- unknown messages

### 8.3 Tracker API

```text
GET    /api/trackers
POST   /api/trackers
GET    /api/trackers/:id
PATCH  /api/trackers/:id
DELETE /api/trackers/:id
POST   /api/trackers/:id/pause
POST   /api/trackers/:id/resume
POST   /api/trackers/:id/recheck
GET    /api/trackers/:id/history
```

### 8.4 Extraction API

```text
POST /api/extract/preview
POST /api/extract/confirm
```

#### Request: `POST /api/extract/preview`

```json
{
  "url": "https://www.zara.com/...",
  "initial_price": "199.99",
  "currency": "PLN"
}
```

#### Response: `POST /api/extract/preview`

```json
{
  "title": "Product title",
  "image_url": "https://...",
  "stock_status": "in_stock",
  "candidates": [
    {
      "price": "199.99",
      "currency": "PLN",
      "confidence": 0.91,
      "label": "Main price",
      "rule": {
        "type": "json_ld",
        "path": "offers.price"
      }
    }
  ]
}
```

---

## 9. Price and Stock Extraction Strategy

### 9.1 Extraction Priority

Use this order:

1. JSON-LD Product data
2. Schema.org microdata
3. OpenGraph/product meta tags
4. Known Zara-specific extraction rules
5. Common CSS selectors
6. Regex fallback over HTML

### 9.2 Zara-Specific MVP Handling

Create a dedicated Zara extractor module.

Required behavior:

- Detect product title
- Detect current price
- Detect currency
- Detect stock status if available
- Detect product image if available
- Store extraction rule for future checks

Do not hardcode only one Zara domain. Zara has regional domains and paths. The extractor should be domain-aware and locale-tolerant.

### 9.3 Generic Extraction Fallback

Generic extractor should search for:

- JSON-LD `Product`
- `offers.price`
- `offers.priceCurrency`
- `availability`
- `itemprop="price"`
- common class names containing `price`
- text patterns that look like prices

### 9.4 User Confirmation

When creating a tracker:

1. User enters URL, visible current price, and currency.
2. Backend fetches page and generates candidates.
3. If candidate price matches user-entered price, allow tracker creation.
4. If several candidates exist, ask user to select the correct one.
5. If no candidate matches, reject creation or set `needs_confirmation`.

MVP recommendation:

- Do not create active trackers when no price candidate is found.
- Show a clear error instead.

---

## 10. URL Security Requirements

User-provided URLs are dangerous. Implement SSRF protection before any HTTP request.

Required checks:

- Allow only `http` and `https`
- Reject `localhost`
- Reject private IPv4 ranges
- Reject private IPv6 ranges
- Reject link-local addresses
- Reject metadata IPs
- Reject non-standard protocols
- Enforce max URL length
- Enforce HTTP timeout
- Enforce max response body size
- Follow redirects only after validating each redirected URL
- Reject redirects to private/internal addresses

---

## 11. Worker Logic

### 11.1 Selection Query

Use PostgreSQL row locking:

```sql
SELECT *
FROM trackers
WHERE status = 'active'
  AND next_check_at <= now()
ORDER BY next_check_at
LIMIT 100
FOR UPDATE SKIP LOCKED;
```

### 11.2 Worker Steps

For each tracker:

1. Lock tracker.
2. Fetch page safely.
3. Extract price using saved extraction rule.
4. Extract stock status.
5. If saved rule fails, try fallback extraction.
6. Save price point.
7. Save stock point.
8. Compare new price with current price.
9. Compare new stock status with current stock status.
10. Create notification if something changed.
11. Send Telegram notification.
12. Update tracker fields.
13. Set `next_check_at` based on user plan.

### 11.3 Error Handling

If extraction fails:

- Increment `consecutive_errors`
- Save error in `price_points`
- Keep previous known price
- Do not send fake price change notifications
- If repeated failures reach threshold, set tracker to `needs_confirmation`
- Optionally notify user that tracker needs attention

Suggested threshold:

```text
3 consecutive errors -> needs_confirmation
```

---

## 12. Notification Rules

### 12.1 Price Changed

Send when:

```text
new_price != current_price
```

But avoid duplicate notifications:

- Do not send the same notification twice for the same price value.
- Do not send more than one price notification per tracker per check cycle.

### 12.2 Stock Changed

Send when:

```text
new_stock_status != current_stock_status
```

Special notification types:

- `back_in_stock`
- `out_of_stock`

### 12.3 Telegram Message Examples

#### Price Change

```text
Цена изменилась

Товар: {title}
Старая цена: {old_price} {currency}
Новая цена: {new_price} {currency}

Открыть товар:
{url}
```

#### Back in Stock

```text
Товар снова в наличии

Товар: {title}
Цена: {current_price} {currency}

Открыть товар:
{url}
```

#### Out of Stock

```text
Товар закончился

Товар: {title}
Последняя цена: {current_price} {currency}

Открыть товар:
{url}
```

---

## 13. Frontend Pages

### 13.1 Public Pages

Initial Russian pages:

```text
/ru
/ru/pricing
/ru/how-it-works
/ru/faq
/ru/privacy
/ru/terms
```

Future pages:

```text
/en
/en/pricing
/en/how-it-works
/en/faq
/en/privacy
/en/terms

/pl
/pl/pricing
/pl/how-it-works
/pl/faq
/pl/privacy
/pl/terms
```

### 13.2 Dashboard Pages

```text
/[locale]/dashboard
/[locale]/dashboard/trackers
/[locale]/dashboard/trackers/new
/[locale]/dashboard/trackers/:id
/[locale]/dashboard/settings
```

### 13.3 Dashboard Requirements

Tracker list must show:

- Product title
- Product URL/domain
- Current price
- Currency
- Current stock status
- Last checked time
- Status
- Pause/resume action
- Delete action

Tracker detail must show:

- Product title
- URL
- Current price
- Initial price
- Currency
- Current stock status
- Price history
- Stock history
- Last errors if any
- Manual recheck button

---

## 14. Admin / Debug Panel

Minimum admin/debug visibility:

- Total users
- Active trackers
- Failed trackers
- Trackers by domain
- Recent extraction errors
- Recent notifications
- Worker health
- Queue size
- Domains with high failure rate

This can be protected by environment variable admin token in MVP.

---

## 15. SEO Requirements

Initial SEO language: Russian.

Required:

- Server-side rendered landing pages
- Unique title and description per page
- OpenGraph metadata
- Sitemap.xml
- Robots.txt
- Clean canonical URLs
- FAQ page
- Pricing page
- How it works page

Do not create SEO pages promising perfect support for every store.

Safe SEO positioning:

- Telegram price tracker
- Мониторинг цен с уведомлениями в Telegram
- Отслеживание изменения цены товара
- Уведомления о наличии товара

---

## 16. Development Phases

## Phase 0 — Project Setup

### Goals

Create the monorepo, local environment, and database foundation.

### Tasks

- Create repository structure
- Add Go backend module
- Add Next.js frontend
- Add Docker Compose
- Add PostgreSQL
- Add migrations system
- Add config loading
- Add logging
- Add health endpoint

### Acceptance Criteria

- `docker compose up` starts database, backend, frontend
- Backend health endpoint works
- Migrations run successfully

---

## Phase 1 — Telegram Authentication

### Goals

Implement Telegram-only login.

### Tasks

- Create Telegram bot
- Add webhook endpoint
- Implement login challenge creation
- Implement `/start login_<token>` handling
- Create user from Telegram data
- Create session
- Add logout
- Add `/api/me`
- Add frontend login button
- Add frontend session handling

### Acceptance Criteria

- User can log in through Telegram
- User appears in database
- User can open dashboard after login
- User can log out

---

## Phase 2 — Plans and Usage Limits

### Goals

Implement free plan limit before tracker creation.

### Tasks

- Create `plans` table
- Seed `free` and placeholder `pro`
- Add user plan field
- Add active tracker count query
- Enforce max 5 active trackers for free plan

### Acceptance Criteria

- Free user cannot create more than 5 active trackers
- Limit is enforced on backend, not only frontend

---

## Phase 3 — Tracker CRUD

### Goals

Allow users to manage trackers.

### Tasks

- Add tracker database queries
- Add create/list/detail/update/delete endpoints
- Add pause/resume endpoints
- Add dashboard tracker list
- Add tracker detail page
- Add form for URL, current price, and currency

### Acceptance Criteria

- User can create tracker draft
- User can list own trackers
- User cannot access other users' trackers
- User can pause, resume, and delete tracker

---

## Phase 4 — Extraction Preview

### Goals

Extract price and stock status from product pages before tracker activation.

### Tasks

- Implement safe HTTP fetcher
- Implement SSRF protection
- Implement max response size
- Implement generic extractor
- Implement Zara extractor
- Extract title
- Extract image URL
- Extract price candidates
- Extract currency
- Extract stock status
- Return candidates to frontend
- Add user confirmation UI

### Acceptance Criteria

- User submits Zara product URL, price, and currency
- Backend returns at least one price candidate when possible
- User can confirm the correct candidate
- Tracker stores extraction rule
- Tracker stores initial price point
- Tracker stores initial stock point

---

## Phase 5 — Worker and Scheduled Checks

### Goals

Check active trackers every hour.

### Tasks

- Create worker binary
- Implement due tracker query with `FOR UPDATE SKIP LOCKED`
- Implement extraction using saved rule
- Implement fallback extraction
- Save price point on each check
- Save stock point on each check
- Update tracker current price and stock status
- Update `last_checked_at`
- Update `next_check_at`
- Handle repeated errors

### Acceptance Criteria

- Worker checks active trackers
- Worker does not process same tracker concurrently
- Price history is saved
- Stock history is saved
- Failed extraction does not overwrite valid current price
- Repeated failures move tracker to `needs_confirmation`

---

## Phase 6 — Telegram Notifications

### Goals

Notify users when price or stock changes.

### Tasks

- Implement notification creation
- Implement Telegram send function
- Notify on price change
- Notify on stock status change
- Save notification status
- Add retry for failed notifications
- Avoid duplicate notifications

### Acceptance Criteria

- User receives Telegram message when price changes
- User receives Telegram message when stock status changes
- Notification is saved in database
- Failed notification is recorded

---

## Phase 7 — Price and Stock History UI

### Goals

Show tracker history in dashboard.

### Tasks

- Add history endpoint
- Add price history table
- Add stock history table
- Add simple chart
- Add last check/error display

### Acceptance Criteria

- User can view price changes over time
- User can view stock status changes over time
- User can see last check status

---

## Phase 8 — Landing and SEO

### Goals

Create public marketing pages in Russian.

### Tasks

- Create Russian landing page
- Create pricing page
- Create how-it-works page
- Create FAQ page
- Create privacy page
- Create terms page
- Add metadata
- Add OpenGraph
- Add sitemap
- Add robots.txt

### Acceptance Criteria

- Public pages are indexable
- Russian landing explains the product clearly
- Login CTA works
- No exaggerated claims about universal store support

---

## Phase 9 — Admin / Debug Panel

### Goals

Provide operational visibility.

### Tasks

- Add protected admin route
- Show user count
- Show active tracker count
- Show failed tracker count
- Show recent extraction errors
- Show domain failure stats
- Show recent notifications
- Show worker health info

### Acceptance Criteria

- Admin can identify broken domains
- Admin can see whether worker is running
- Admin can inspect extraction failures

---

## Phase 10 — Deployment

### Goals

Deploy MVP to production-like VPS environment.

### Tasks

- Prepare production Docker Compose
- Configure Caddy/Nginx
- Configure HTTPS
- Configure Telegram webhook URL
- Configure database backups
- Configure logs
- Configure environment secrets
- Add basic monitoring

### Acceptance Criteria

- Production site is reachable over HTTPS
- Telegram webhook works
- Worker runs continuously
- Database persists data
- Backups exist

---

## 17. Testing Plan

### 17.1 Backend Tests

Required:

- Auth challenge tests
- Telegram webhook tests
- Session tests
- Plan limit tests
- Tracker CRUD tests
- URL validation tests
- SSRF protection tests
- Worker logic tests
- Notification logic tests

### 17.2 Extractor Tests

Required:

- Use saved HTML fixtures
- Test Zara product pages from different locales if fixtures are available
- Test JSON-LD extraction
- Test stock status extraction
- Test fallback price detection
- Test extraction failure behavior

### 17.3 Frontend Tests

Required:

- Login flow smoke test
- Add tracker form test
- Confirmation UI test
- Dashboard list test
- Tracker detail page test

---

## 18. Non-Functional Requirements

### 18.1 Performance

MVP target:

- Backend API response under 500ms for normal dashboard actions
- Extraction preview may take several seconds
- Worker must process trackers in batches
- Worker must avoid unbounded concurrency

### 18.2 Reliability

Required:

- Timeouts on all external requests
- Retries only where safe
- Clear error states
- No fake notifications when extraction fails
- Preserve last valid price

### 18.3 Security

Required:

- Secure session cookies
- Hash session tokens in database
- Validate Telegram login challenge
- Expire login challenges quickly
- Protect against SSRF
- Rate-limit login and extraction endpoints
- Never expose Telegram bot token to frontend

---

## 19. Risks and Mitigations

### Risk 1 — Arbitrary Websites Are Unreliable

Mitigation:

- Start with Zara-specific extractor
- Use user-entered current price as validation
- Require confirmation of detected price
- Store extraction rules
- Mark broken trackers as `needs_confirmation`

### Risk 2 — Anti-Bot Protection

Mitigation:

- Use conservative request frequency
- Use proper timeouts
- Avoid aggressive scraping
- Add per-domain throttling
- Do not promise universal support

### Risk 3 — False Price Detection

Mitigation:

- Candidate confirmation UI
- Compare against user-entered current price
- Prefer structured data over regex
- Never notify when confidence is too low

### Risk 4 — Notification Spam

Mitigation:

- Deduplicate same price notifications
- Send max one notification per tracker per check cycle
- Store notification history

### Risk 5 — Future Payments Need Verified Identity

Mitigation:

- Use Telegram-only verified identity from start
- Store stable `telegram_user_id`
- Keep `plans` table from MVP
- Add subscription tables later

---

## 20. Agent Implementation Rules

An AI coding agent working on this project must follow these rules:

1. Do not implement payments in MVP.
2. Do not remove SSRF protection.
3. Do not make frontend-only limit checks; limits must be enforced in backend.
4. Do not overwrite a valid current price with null or failed extraction result.
5. Do not send price change notifications when extraction fails.
6. Do not promise support for all websites in marketing copy.
7. Do not hardcode Zara as the only possible domain; create extractor interfaces.
8. Do not store raw session tokens in database; store token hashes.
9. Do not expose Telegram bot token to frontend.
10. Do not add Kubernetes or microservices for MVP.
11. Keep Russian as first UI language, but structure routes for English and Polish later.
12. Keep currencies as user-selected strings; do not convert currency values.

---

## 21. Suggested First Coding Order

Use this exact order:

1. Backend project setup
2. PostgreSQL migrations
3. Telegram bot webhook
4. Telegram login challenge
5. Sessions
6. Basic frontend login
7. Dashboard shell
8. Plans and free limit
9. Tracker CRUD
10. Safe URL fetcher
11. Zara extractor
12. Generic extractor
13. Extraction preview UI
14. Tracker confirmation flow
15. Worker hourly checks
16. Price and stock history
17. Telegram notifications
18. Landing page
19. Admin/debug panel
20. Production Docker deployment

---

## 22. Definition of MVP Done

MVP is done when:

- A user can log in with Telegram.
- A user can add up to 5 Zara product trackers.
- User enters current price and currency when adding a tracker.
- Service detects and confirms the product price.
- Service detects stock status if possible.
- Worker checks products every hour.
- Price history is saved.
- Stock history is saved.
- Telegram notification is sent when price changes.
- Telegram notification is sent when stock status changes.
- User can see tracker history in dashboard.
- Landing page explains the product in Russian.
- Basic admin/debug panel shows failures.
- App runs in Docker Compose.
- Production deployment supports HTTPS and Telegram webhook.
