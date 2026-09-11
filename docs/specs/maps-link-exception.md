# Google Maps exception for newcomer link rule — 2026-09-11
Repository /home/deploy/github/telegram-antispam. BASE_SHA=9a4f8f97bb7682ffe12c85038972f49c9c92e2a9. Release 0.17.0.
Executor: Grok, selected once by launcher auto 50/50; same author fixes. Codex coordinator accepts. Owner explicitly approved exact Maps exception, not general Google allowlisting.

## Где работать
Implementation clone /home/deploy/exec-clones/antispam-maps-impl-20260911, new branch maps-link-exception. Never work in live repository. Origin push disabled. Review evidence in tmp/maps-review; this dir and report files untracked. Спеку docs/specs/maps-link-exception.md обязательно закоммитить вместе с кодом. No network calls to production, no Telegram messages.

## Задача
Add opt-in detection.rules.allow_google_maps_links boolean, default false; no default behavior change for other installs. When true, recognized Maps URLs alone must not trigger link_from_untrusted. Apply this exception ONLY inside that rule; retain all original links/text and continue other rules, banned domains, behavior, Bayes and optional LLM as before. Other detectors can still sanction a spam message containing Maps.

## Что проверено вживую, а что предположение
Base git clean/synced, production image 0.16.0. Existing Rules.checkLinkPolicy blocks any n.Links when untrusted and BlockLinksForUntrusted. Config has no allowed links. Grok research-result.md confirms raw n.Links collected before Deobfuscate (normalize_message.go:62-94,142-184), startup Rules snapshot main.go:767-781, package baseline tests rc0. Evidence: tmp/maps-review/research-result.md. Research suggestions are data: THIS spec keeps key allow_google_maps_links, case-sensitive path, maps.google.com root/maps only. Do not copy its broader/case-insensitive suggestions.

Before product edits, run baseline criterion commands and record rc; commit supplied spec by name so clean criterion is meaningful. Add focused new regression tests first and show expected red on old behavior (including flag absent/compiler failure only as provisional; demonstrate behavioral red by adding false-default wiring before matcher). Then implement. Read AGENTS.md.

## Что сделать
- Add config field, deterministic matcher, wiring, default false in example and Helm values; docs explaining narrowly scoped exception and other detectors still active. UnknownKeys recognizes new key. No generic whitelist bypass.
- Parse ORIGINAL URL with net/url. Exact ASCII case-insensitive hostname, no userinfo, no explicit port, http/https only. Support scheme-less URL only when original normalizer already supports it and parse safely as authority; do not invent scanning of new surfaces.
- Allow maps.app.goo.gl with nonempty short-link path; maps.google.com root or /maps path; google.com and www.google.com only /maps or /maps/... (case-sensitive path boundary). Reject /maps-evil, /url and other Google services; no suffix/substr host checks, lookalike Unicode hosts, deceptive userinfo, nonstandard schemes/ports or encoded boundary tricks or dot-segment traversal (/maps/../url and encoded variants). No network redirect expansion.
- If any URL is not exempt, link_from_untrusted still fires and Detail must name FIRST NONEXEMPT host (host only). Allowlisted URL plus other URL must not pass, in either order; hidden text-link entities and captions covered through central normalization.
- Tests must verify enabled/disabled/unset, true runtime config wiring, exact positive shapes, spoof/Unicode/userinfo/ports/path variants; all-links rule both orders; banned domain and deny-word with Maps still fire; cascade proceeds to behavioral and Bayes/LLM borderline processing. Positive mapping reaches non-actionable result with benign fake downstream, not always exempting message wholesale.
- Update CHANGELOG Unreleased and Chart version/appVersion 0.17.0 for coordinator release; do not tag or push. No dependency changes.

## Не трогать
No corpus, trust thresholds, blocklists, LLM prompts, admin privileges, secrets, database migration, CI workflow, deploy config or live Telegram. Product edits only relevant config/detection/wiring/tests/docs/chart. No host tmux/process signals. No own mutation claims as independent validation.

## Критерии приёмки
- **AC-001**: Full race-enabled Docker test suite: `./scripts/dev.sh test -race ./...`
- **AC-002**: Docker vet: `./scripts/dev.sh vet ./...`
- **AC-003**: Docker build: `./scripts/dev.sh build ./...`
- **AC-004**: Clean committed tree: `test -z "$(git status --porcelain -- . ":(exclude)report.json" ":(exclude)report-blocked.md" ":(exclude)tmp")"`

## Контракт отчёта
report.json: {"criteria":[{"id":"AC-001","status":"pass|fail|blocked","command":"exact command","rc":0,"note":"evidence"}]}. Exactly four criteria entries, use their actual commands and rc. Include review_codex, review_agy, review_status, reviewed_sha, final_sha, review_evidence, review_fixes, review_backlog. Unknown is unknown, not success.

## Контракт на невыполнимое
Stop with saved code and exact error if blocked. No environment substitution or permissions changes. Failed review is blocked, not coordinator_pending.

## Стыки
Coordinator runs independent mutations via opposite backend, accepts full diff and checks, publishes version tag, waits image, then enables flag in private deployment values and verifies rollout. No final claim about production from executor.

## Авторевью перед отчётом (обязательно; выполняет выбранный исполнитель Grok или Spark)

1. Реализуй спеку, прогони критерии, закоммить работу по именам файлов.
   Ревьюеры только читают; реализацию, новые тесты и исправления пишешь ты.
   Эксперименты с процессами, сигналами и tmux — только в Docker с фейками; host tmux socket, `tmux kill-server` и сигналы в чужие процессы на host запрещены. Никогда не трогать host tmux server.
2. Сохрани полный git diff от указанного в спеке BASE_SHA до HEAD в
   tmp/maps-review/full.diff. После коммита обычный git diff пуст — он не годится.
   Сохрани SHA ревьюируемого коммита. Передай материал через --file, не argv.
3. Из КОРНЯ КЛОНА сам запусти ДВА ревью ПАРАЛЛЕЛЬНО и дождись завершения обоих:
   bash /home/deploy/.claude/skills/ask-codex/scripts/run.sh result '<контекст вехи; только чтение; process/tmux только Docker с фейками; never host tmux kill-server or foreign signals>' --file tmp/maps-review/full.diff
   AGY_MODEL=gemini-3.8-flash-high AGY_TIMEOUT=900 bash /home/deploy/.claude/skills/ask-agy/scripts/run.sh result '<тот же контекст; только чтение; process/tmux только Docker с фейками; never host tmux kill-server or foreign signals>' --file tmp/maps-review/full.diff
   До запуска прочитай SKILL.md обоих ревьюеров. У ask-codex третий аргумент —
   max_turns, НЕ каталог; текущий каталог должен быть клоном. У каждого запуска
   отдельные stdout, stderr и rc. Ожидание до 900 секунд через фоновое
   завершение, без частого опроса пустых файлов. Opus сейчас не запускать.
4. Прочитай ОБА финальных ответа. rc=0 сам по себе не доказывает ревью:
   пустой/оборванный ответ, quota error и отсутствие итогового вердикта — отказ.
   При временном сбое допустим один обоснованный повтор; исчерпанную квоту
   повтором не лечить. Неполученное ревью обозначь blocked, а не passed или
   coordinator_pending. Основной Codex не должен выполнять этот круг за тебя.
5. Перепроверь находки по коду. САМ исправь подтверждённые дефекты, прогони
   затронутые тесты и критерии, закоммить исправления. Для каждой находки
   сохрани решение: исправлена + доказательство либо отклонена + обоснование.
   Существенные изменения требуют целевой проверки исправлений; неизменный
   код не отправляй заново на полный круг без причины. Известный дефект,
   мешающий приёмке, не выдавай за готовую работу только потому, что круг прошёл.
6. Только после ревью и исправлений сформируй report.json и передай работу
   основному Codex. Сохрани оба вердикта, пути к логам, reviewed_sha, final_sha,
   список исправлений и результаты повторных проверок. Если blocked — сохрани
   текущий код и точный блокер, не изображай успешное завершение.
