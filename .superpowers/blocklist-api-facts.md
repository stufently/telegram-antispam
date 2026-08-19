# LOLS + CAS blocklist API facts (probed live 2026-08-18)

Both services expose bulk newline-delimited int64 user-ID lists (mirrorable) AND a
per-user JSON lookup. Everything below was verified by live curl/OpenAPI probe.

## CAS (Combot Anti-Spam) — api.cas.chat
- **Full bulk export:** `GET https://api.cas.chat/export.csv` → plain text, one banned
  Telegram user_id (int64) per line, no header. ~1.2M ids. This is the CAS mirror source.
  (Despite the `.csv` name it is a single-column newline list, e.g. `5284995823\n8046042909\n...`.)
- **Per-user:** `GET https://api.cas.chat/check?user_id=<id>` → JSON. Not-banned returns
  `{"ok":false,"description":"Record not found."}`; a banned id returns `ok:true` with a
  result. GET only, HTTPS only. (Not used in the hot path — the mirror covers it.)
- No delta feed; refresh by re-fetching export.csv in full.

## LOLS — api.lols.bot (OpenAPI at https://api.lols.bot/lols-bot.json, v1.0.0)
- **List catalog:** `GET https://api.lols.bot/lists` → JSON array of `{id, description,
  format:{json?, csv?, plain?}}`. Live catalog:
  - `spammers-full` — "Hourly updated whole banlist" → plain `https://lols.bot/spam/banlist.txt`
    (also `.json`). The full ~3.9M mirror. **Refreshed hourly upstream.**
  - `spammers-1h` — "Hourly updated banlist for last hour" → plain
    `https://lols.bot/spam/banlist-1h.txt` (also `.json`). **The hourly DELTA** (ids newly
    banned in the last hour). Small (~10k lines in a sample).
  - `scammers` — "Verified scammer list" → plain `https://lols.bot/scammers.txt`
    (also `.json`, `.csv`).
- All `plain` lists are one int64 user_id per line, no header (same shape as CAS export.csv).
- **Per-user:** `GET https://api.lols.bot/account?id=<int64>[&quick=true]` → JSON
  `AccountStatus{ok:bool, user_id:int64, banned:bool, when?, offenses?, spam_factor?,
  scammer?, scamrsalert?}`. `banned:true` = listed. `ok` is API health, always true on a
  response. (Not used in the hot path for v1.)

## Design implications (for the plan)
- **Mirror, not per-message lookup.** Hold the union of {LOLS spammers-full, CAS export.csv}
  in memory; membership lookups are local (no per-message network — matches the "much
  simpler" + "fail-open, never blocks the chat" goal). Per-user APIs are a possible later
  fallback, out of v1 scope.
- **Storage:** a sorted `[]int64` snapshot (≈4.86M ids × 8B ≈ 39MB) with binary-search
  lookup, swapped atomically on refresh. Far cheaper than a `map[int64]struct{}` (~250MB)
  and rebuild-friendly. "Bounded in-memory cache" per spec §5.3.
- **Sync cadence:** full refresh of both sources on a long interval (e.g. 6h) merged +
  sorted + deduped into a new snapshot; between full refreshes, poll LOLS `spammers-1h`
  hourly and merge the delta into the snapshot so new bans land within the hour without
  re-downloading ~5M ids. All intervals config-driven.
- **Fail-open (spec §5.3):** the snapshot starts empty; lookups on an empty/not-yet-loaded
  snapshot return "not listed" (never block). A failed fetch keeps the last good snapshot
  (never clears it). A blocklist outage must never block a chat.
- **Parsing:** trim each line, skip blanks/non-numeric, `strconv.ParseInt(_, 10, 64)`.
  Both endpoints are HTTPS GET, no auth. Set a short HTTP timeout; stream/scan line-by-line
  to avoid holding the whole body + parsed slice simultaneously where practical.
- **Cascade placement (ruling):** blocklist hit is a cheap authoritative hard signal → run
  it early, right after the §4 admin-immunity gate and before/with hard rules, for
  non-trusted senders (consistent with the trust-gate skipping established members). A hit
  ⇒ actionable `Signal{Name:"blocklist"}` (or `cas`/`lols` detail). Config-gated
  (`blocklist.enabled`), fail-open.
