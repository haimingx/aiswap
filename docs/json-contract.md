# JSON Output Contract

All machine-readable commands (`status --json`, `list --json`, `switch --json`, `models --json`, `stats --json`) write a single JSON document to `stdout`. Human notices and diagnostics always go to `stderr`.

## Versioning & Compatibility

- Every payload includes `"schemaVersion": 1`.
- Changes within v1 are strictly additive; consumers must ignore unknown fields.
- Handled errors emit `{"schemaVersion": 1, "error": {"code": "...", "message": "..."}}` to `stdout` and exit non-zero.

## Status Payload (`aiswap status --json`)

```json
{
  "schemaVersion": 1,
  "generatedAt": "2026-11-22T05:00:00Z",
  "providers": [
    {
      "provider": "claude",
      "installed": true,
      "activeSlot": 1,
      "liveEmail": "user@example.com",
      "livePlan": "Max 5x",
      "liveQuotaStatus": "ok",
      "liveQuota": {
        "windows": [
          {"name": "5h", "usedPct": 24.5, "resetsAt": "2026-11-22T09:00:00Z"},
          {"name": "7d", "usedPct": 41.0, "resetsAt": "2026-11-27T00:00:00Z"}
        ],
        "fetchedAt": "2026-11-22T04:58:00Z",
        "stale": false
      },
      "managed": true,
      "accounts": [
        {
          "slot": 1,
          "alias": "work",
          "email": "user@example.com",
          "plan": "Max 5x",
          "active": true,
          "quotaStatus": "ok",
          "quota": {
            "windows": [
              {"name": "5h", "usedPct": 24.5, "resetsAt": "2026-11-22T09:00:00Z"},
              {"name": "7d", "usedPct": 41.0, "resetsAt": "2026-11-27T00:00:00Z"}
            ],
            "fetchedAt": "2026-11-22T04:58:00Z",
            "stale": false
          }
        }
      ]
    }
  ]
}
```

## Switch Payload (`aiswap switch <provider> [target] --json`)

```json
{
  "schemaVersion": 1,
  "provider": "claude",
  "switched": true,
  "fromSlot": 1,
  "toSlot": 2,
  "toEmail": "me@example.com",
  "reason": "switched"
}
```

## Models Payload (`aiswap models --json`)

```json
{
  "schemaVersion": 1,
  "generatedAt": "2026-11-22T07:00:00Z",
  "providers": [
    {
      "provider": "agy",
      "email": "user@gmail.com",
      "models": [
        {"id": "gemini-2.5-pro", "displayName": "Gemini 2.5 Pro", "pool": "Gemini Pool", "usedPct": 1.0},
        {"id": "claude-opus-5-5", "displayName": "Claude Opus 5.5", "pool": "Claude / GPT Pool", "usedPct": 50.0}
      ]
    }
  ]
}
```

## Stats Payload (`aiswap stats --days 7 --json`)

```json
{
  "schemaVersion": 1,
  "generatedAt": "2026-11-22T05:00:00Z",
  "days": 7,
  "timezone": "CST",
  "pricing": {
    "source": "cached",
    "modelCount": 221,
    "updatedAt": "2026-11-22T04:55:00Z"
  },
  "totals": {
    "requests": 120,
    "inputTokens": 210000,
    "outputTokens": 85000,
    "cacheReadTokens": 45000000,
    "estimatedCostUSD": 11.42
  },
  "providers": [
    {
      "provider": "claude",
      "totals": {
        "requests": 120,
        "inputTokens": 210000,
        "outputTokens": 85000,
        "cacheReadTokens": 45000000,
        "estimatedCostUSD": 11.42
      },
      "models": [
        {"model": "claude-opus-5-5", "requests": 120, "inputTokens": 210000, "outputTokens": 85000, "cacheReadTokens": 45000000, "estimatedCostUSD": 11.42}
      ],
      "daily": [
        {"date": "2026-11-22", "requests": 120, "inputTokens": 210000, "outputTokens": 85000, "cacheReadTokens": 45000000, "estimatedCostUSD": 11.42}
      ]
    }
  ]
}
```

`estimatedCostUSD` is omitted when no pricing rate matches the model (e.g. `unknown`), and rounded to 6 decimal places when present.
