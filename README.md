<div align="center">

# One API Future

**English** | [简体中文](./README.zh-CN.md)

_✨ An internal-use, customized fork of [songquanpeng/one-api](https://github.com/songquanpeng/one-api) ✨_

</div>

> **About this project**: this is a customized fork of [songquanpeng/one-api](https://github.com/songquanpeng/one-api) (MIT License), tailored for **internal company use** with feature additions/removals and simplified billing. It is open-sourced under the original MIT license. The original upstream README is kept as [README-ORIGINAL.md](./README-ORIGINAL.md).
>
> Baseline: one-api main branch snapshot (v0.0.0). Tech stack unchanged: Go (Gin + GORM) backend + React (Semantic UI) frontend, with the frontend embedded into the Go binary at build time.

---

## New Features

### 1. Scheduled Channel Enable / Disable
- Each channel can be configured with a daily "disable time ~ enable time" window; a background job switches status every 30 seconds
- **Windows crossing midnight are supported** (e.g. disable at 23:00, enable at 06:00) — ideal for upstreams only available during certain hours
- Only affects manual enable/disable status; it **never overrides** the auto-disabled state caused by failed channel tests (status 3), so it does not conflict with failure detection
- Configure via the "Schedule" button on each channel row; enabled channels show a time-window badge next to their name

### 2. Global Model Alias (Unified Model Name)
- Operation Settings → "Model Alias / Unified Model Name", configured as JSON: `{"unified model name": "actual backend model"}`
- Users (AI agents) always use the unified name; **when the backend model changes, only the mapping needs updating — client configs stay untouched**
- The gateway resolves the alias before channel selection; billing, logging and retries all use the real model. Aliases stack with per-channel "model mapping" (channel-level wins)
- `GET /v1/models` lists aliases alongside their available real models so agents can discover them automatically

### 3. OpenAI Responses API Support (`POST /v1/responses`)
- Pass-through proxying to upstream channels (no protocol conversion), with streaming SSE (chunked, flushed in real time) and non-streaming modes
- Only channels with "Support Responses API" checked in the edit page are used; others return a clear `responses_not_supported` error instead of a confusing upstream 404
- Model names in requests go through the same global alias/mapping before forwarding
- **Usage is metered**: tokens are extracted from the response (non-streaming JSON `usage`, or the `response.completed` event when streaming) and billed with the same formula (quota = prompt + completion × completionRatio), recorded in consume logs and user/channel usage stats

### 4. Simplified Billing for Internal Use
- Model, completion and group ratios are **fixed to 1**; quota equals raw token count, logs show `Ratio: 1.00 × 1.00 × 1.00`
- User quota is treated as **unlimited**: user quota is no longer checked or deducted; usage is tracked via consume logs and user request statistics
- Token quotas can still be set for per-token usage control (requests are rejected once exhausted); **new tokens default to unlimited quota**

## Removed (not needed for internal use)

- "Redemption" and "Top-up" navigation entries and the `/topup` top-up page
- Operation Settings cards: "Quota Settings", "Ratio Settings", "Top-up Link", "Quota Reminder Threshold"
- Low-quota reminder emails (user quota is unlimited now)

> The redemption management page is still reachable at `/redemption` (no navigation entry); disable the route yourself if it is not needed.

## Deployment

### Option 1: Static Binary + Interactive Installer (recommended, simplest)

Linux amd64 static binary (musl statically linked, zero dependencies):

```bash
mkdir -p /opt/one-api && wget https://github.com/maskerTUI/one-api-future/releases/latest/download/one-api-deploy.tar.gz
mkdir -p /opt/one-api && tar xzf one-api-deploy.tar.gz -C /opt/one-api && cd /opt/one-api
chmod +x install.sh start.sh
sudo ./install.sh
```

The installer asks (pressing Enter everywhere = all defaults):
1. **Database**: SQLite (Enter, zero config) or MySQL (asks host/port/db/user/password one by one; password must not contain `@ : / space`)
2. **Service port**: default 3000
3. **Start on boot**: default yes, registers a systemd service

When finished it prints the access URL and the initial account (`root` / `123456` — change it immediately). Re-run `sudo ./install.sh` at any time to change settings.

### Option 2: Docker

```bash
docker build -t one-api-future .
docker run -d --name one-api-future --restart always -p 3000:3000 \
  -e TZ=Asia/Shanghai -v /your/data/path:/data \
  one-api-future
# To use MySQL, append:
#   -e SQL_DSN="user:password@tcp(mysql-host:3306)/one-api"
```

### Option 3: Build from Source

```bash
# Frontend (output is moved to web/build/default and embedded into the Go binary)
cd web/default && npm install && DISABLE_ESLINT_PLUGIN=true npm run build && cd ../..

# Backend (SQLite requires CGO)
CGO_ENABLED=1 go build -o one-api .
# Cross-compile a static Linux binary (using zig as the C compiler):
# CGO_ENABLED=1 GOOS=linux GOARCH=amd64 CC="zig cc -target x86_64-linux-musl" go build -o one-api-linux-amd64 .
```

Environment variables:

| Variable | Description | Default |
| --- | --- | --- |
| `PORT` | Service port | `3000` |
| `SESSION_SECRET` | Session secret (set a random value in production) | random |
| `SQL_DSN` | MySQL DSN `user:pass@tcp(host:3306)/db`; falls back to SQLite when unset | - |
| `LOG_SQL_DSN` | Separate database for log tables (optional) | same as main |
| `SQLITE_PATH` | SQLite file path | `./one-api.db` |
| `CHANNEL_TEST_FREQUENCY` | Channel auto-test interval in seconds; disabled when unset | - |
| Others | See the environment variables section of [README-ORIGINAL.md](./README-ORIGINAL.md) | - |

> Note: SQLite requires CGO. The first start downloads tiktoken vocab files from the internet (1-2 minutes); for offline environments set `TIKTOKEN_CACHE_DIR` as described in the original README.

## Key Changed Files

| Feature | Files |
| --- | --- |
| Scheduled channel enable/disable | `controller/channel_schedule.go`, `model/channel.go`, `web/.../ChannelsTable.js` |
| Global model alias | `model/alias.go`, `middleware/distributor.go`, `model/option.go`, `web/.../OperationSetting.js` |
| Responses API pass-through + billing | `relay/controller/responses.go`, `router/relay.go` |
| Simplified billing | `relay/billing/ratio/{model,group}.go`, `model/token.go` |
| UI simplification | `web/.../Header.js`, `OperationSetting.js`, `App.js`, `Token/EditToken.js` |

## License

MIT © original author [songquanpeng](https://github.com/songquanpeng/one-api) and contributors of this fork. All modifications in this repository are released under the MIT license as well; see the original [LICENSE](./LICENSE).
