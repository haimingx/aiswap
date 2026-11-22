# Provider Protocols

Verified against local installations and upstream behavior. All values below describe schema structure only.

## 1. Claude Code (`claude`)

### Credential & Config Storage
- **Linux**: `~/.claude/.credentials.json` (`0600`) + `~/.claude.json`
- **macOS**: Keychain service `Claude Code-credentials` (OAuth) + `~/.claude.json`
- **Env overrides**: `CLAUDE_CONFIG_DIR`, `CLAUDE_SECURESTORAGE_CONFIG_DIR`

### Verified Schema (`.credentials.json`)
```json
{
  "claudeAiOauth": {
    "accessToken": "string",
    "refreshToken": "string",
    "expiresAt": 0,
    "refreshTokenExpiresAt": 0,
    "scopes": ["string"],
    "subscriptionType": "string",
    "subscriptionTypeSource": "string",
    "rateLimitTier": "string"
  },
  "mcpOAuth": {}
}
```
- **Account-owned keys** (swapped): `claudeAiOauth`, `trustedDeviceToken`
- **Machine-shared keys** (always preserved from live): `mcpOAuth`, `mcpOAuthClientConfig`, `mcpXaaIdp`, `mcpXaaIdpConfig`, `pluginSecrets`
- **Config metadata** (`~/.claude.json` → `oauthAccount`): `accountUuid`, `emailAddress`, `organizationUuid`, `organizationName`, `organizationType`, `billingType`, `organizationRateLimitTier`

### Native Locks
`proper-lockfile` directory locks (`mkdir` mutex + mtime heartbeat):
1. `<config-home>/.oauth_refresh.lock` (stale 60s)
2. `<config-home>.lock` (stale 60s)
3. `~/.claude.json.lock` (stale 10s)

### Endpoints & Local Analytics
- Refresh: `POST https://platform.claude.com/v1/oauth/token` (`grant_type=refresh_token`)
- Quota: `GET https://api.anthropic.com/api/oauth/usage` (rolling ~28–30 req/hr limit → enforced ≥180s floor in `UsageStore`)
- Models: `GET https://api.anthropic.com/v1/models?limit=50` (on-demand only)
- Local analytics: `${CLAUDE_CONFIG_DIR:-~/.claude}/projects/*/*.jsonl` (deduplicated by `requestId`)

---

## 2. Codex CLI (`codex`)

### Credential & Config Storage
- **Linux / macOS**: `${CODEX_HOME:-~/.codex}/auth.json` (`0600`)

### Verified Schema (`auth.json`)
```json
{
  "auth_mode": "string",
  "OPENAI_API_KEY": null,
  "tokens": {
    "id_token": "jwt",
    "access_token": "string",
    "refresh_token": "string",
    "account_id": "string"
  },
  "last_refresh": "rfc3339"
}
```
- **Identity**: Parsed locally from `tokens.id_token` JWT claims (`email`, `sub`, `https://api.openai.com/auth` plan/org) + `tokens.account_id`, requiring zero network calls.
- **Preserved files**: `config.toml`, `sessions/`, `history.jsonl`, `state_*.sqlite`, `rules/`, `skills/`.

### Native Locks
- `${CODEX_HOME:-~/.codex}/auth.json.lock`
- `${CODEX_HOME:-~/.codex}/auth.lock`

### Endpoints & Local Analytics
- Quota: `GET https://chatgpt.com/backend-api/wham/usage` (`Authorization: Bearer <access_token>`, `ChatGPT-Account-Id: <account_id>`, `originator: codex_cli_rs`) reporting 5h / 7d windows.
- Models: `GET https://chatgpt.com/backend-api/codex/models?client_version=<detected>` (on-demand; falls back to local `models_cache.json`).
- Local analytics: `${CODEX_HOME:-~/.codex}/sessions/*/*/*/*.jsonl`

---

## 3. Antigravity (`agy`)

### Multi-Target Design (`SyncTarget`)
AGY separates shared account/quota logic (`agy.go`, `quota.go`) from storage targets (`target_cli.go`), allowing future IDE `state.vscdb` sync without changing core or CLI contracts.

### Verified CLI Storage
- **macOS**: Keychain item `service="gemini"`, `account="antigravity"` (wrapped with `go-keyring-base64:` per `zalando/go-keyring`; falls back to `~/.gemini/antigravity-cli/antigravity-oauth-token`)
- **Linux**: `~/.gemini/antigravity-cli/antigravity-oauth-token` (`0600`)

### Verified CLI Schema (`antigravity-oauth-token`)
```json
{
  "token": {
    "access_token": "string",
    "token_type": "string",
    "refresh_token": "string",
    "expiry": "rfc3339",
    "expires_in": 0
  },
  "auth_method": "string",
  "id_token_email": "string"
}
```
- **Identity**: `id_token_email` directly provides the account email offline.
- **Native Locks**: Guarded by `aiswap`'s provider lock + atomic `rename`.
- **Quota, Plan & Models**:
  - `POST https://daily-cloudcode-pa.googleapis.com/v1internal:loadCodeAssist` (resolves `cloudaicompanionProject` and `paidTier` / `currentTier`)
  - `POST https://daily-cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary` (per-pool `5h` and `7d` weekly quota windows)
  - `POST https://daily-cloudcode-pa.googleapis.com/v1internal:fetchAvailableModels` (binding window fallback + HTTP model list fallback)
  - `agy models` CLI subcommand (preferred source for live active model list when `agy` is in `PATH`, overlaid with shared pool `5h` / `7d` usage)
- **Local analytics**:
  - CLI: `${AGY_DATA_DIR:-~/.gemini/antigravity-cli}/brain/*/.system_generated/logs/transcript.jsonl`
  - IDE: `~/.gemini/antigravity/brain/*/.system_generated/logs/transcript.jsonl`

---

## Offline Token Analytics (`aiswap stats`)

`aiswap stats` computes daily and cumulative token usage (`requests`, `input`, `output`, `cacheRead`, `cacheWrite`) **offline with zero API calls** by scanning local JSONL session logs on the current machine:

| Provider | Log Sources | Notes |
|---|---|---|
| **Claude Code** | `${CLAUDE_CONFIG_DIR:-~/.claude}/projects/*/*.jsonl` | Deduplicated by `requestId`; covers CLI and IDE extensions sharing `~/.claude` |
| **Codex CLI** | `${CODEX_HOME:-~/.codex}/sessions/*/*/*/*.jsonl` | Parses `token_count` / `last_token_usage` events; covers CLI and IDE extensions sharing `~/.codex` |
| **Antigravity** | `${AGY_DATA_DIR:-~/.gemini/antigravity-cli}/brain/*/.system_generated/logs/transcript.jsonl`<br>`~/.gemini/antigravity/brain/*/.system_generated/logs/transcript.jsonl` | Aggregates both `agy` CLI and Antigravity IDE transcripts |

**Scope**: Only local coding CLI / Agent logs are parsed. Cloud-only web/desktop chat apps (e.g., ChatGPT Desktop, Claude Desktop) do not write local per-request token JSONL logs and are intentionally out of scope.
