# Architecture

`aiswap` is a local-only multi-account switcher, quota monitor, and interactive TUI for AI coding CLIs (`claude`, `codex`, `agy`). It never runs an API proxy or intercepts model traffic.

## Dependency Direction

Dependencies flow strictly downward. Leaf packages import no internal packages.

```text
cmd/aiswap  ──▶  internal/tui        ──▶  internal/engine  ──▶  internal/provider/{claude,codex,agy}
     │                                          │                              │
     ├──▶ internal/updater                      ├──▶ internal/store            ├──▶ internal/keychain
     └──▶ internal/analytics                    └──▶ internal/contract         ├──▶ internal/lock
                                                        │                      └──▶ internal/fsutil
                                                        ▼
                                                 internal/domain (pure types, zero internal deps)
```

## Core Invariants

1. **Fixed lock order**: `aiswap` store lock → provider native credential lock → provider native config lock. Never reverse.
2. **No network under locks**: Identity lookups and quota HTTP requests run outside all local locks. The sole deliberate exception is refreshing an active live credential inside the provider's own credential lock.
3. **Transactional switching**: Every switch backs up the outgoing login (after verifying ownership), writes the target credential, and updates `accounts.json`; failures roll back the live credential.
4. **One-time refresh token safety (Consume Gate)**: Refreshing a stored slot is serialized per `(provider, slot)`. Once a refresh POST succeeds, the rotated credential is always persisted or stashed in `unclaimed/` so a successor token is never dropped.
5. **Field-level isolation**: Only account-owned fields are swapped. Machine-shared fields (e.g., Claude `mcpOAuth`, `pluginSecrets`, user settings, chat history) are preserved intact.
6. **Lease-based UsageStore**: All surfaces (CLI, TUI, Watch) share `usage.json` with a `claimUntil` lease and per-provider backoff. Fetch failures keep `lastGood` (stale-on-error) instead of blanking the UI.
7. **Zero-touch real store in tests**: Unit tests run exclusively against `t.TempDir()`, in-memory fake keychains, and `httptest.Server`.

## State on Disk

Default data root (override with `AISWAP_DATA_DIR`):
- Linux: `${XDG_DATA_HOME:-~/.local/share}/aiswap/`
- macOS: `~/Library/Application Support/aiswap/`

| Path | Purpose |
|---|---|
| `accounts.json` | Slots per provider, identity metadata, aliases, active slot |
| `credentials/` | Backup credentials on Linux (`0600`); macOS uses Keychain service `aiswap.<provider>` |
| `usage.json` | Shared quota cache (`lastGood`, `claimUntil`, backoff, last error) |
| `unclaimed/` | Stashed rotated credentials that failed normal slot persistence |
