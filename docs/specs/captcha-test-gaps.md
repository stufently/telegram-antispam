# Веха M2-tg: закрыть выживших мутантов капчи (только тесты)

- Репозиторий `/home/deploy/github/telegram-antispam`, дата 26.09.2026.
- BASE_SHA: `909796a3ad74edaba498d63240b14f86a75d355e` (финал M2b, ветка `captcha-m2b`). Клон снят ровно с
  BASE. Спека в клоне не лежит: читай по абсолютному пути
  `/home/deploy/github/telegram-antispam/docs/specs/captcha-test-gaps.md`. Контекст: `captcha-m2a.md`,
  `captcha-m2a-fix.md`, `captcha-m2b.md` там же.
- Исполнитель: `gk` (Grok). Мутации — координатор (Codex).
- Критериев: 6.

## 0. Где работать

Клон `/home/deploy/exec-clones/antispam-captcha-tg-20260926`, ветка `captcha-test-gaps`. Живое дерево не
трогать, push запрещён, `git add` только по именам. `.gopath/` постановщик положил. `tmp/` — для улик.

## 1. Что проверено вживую, а что предположение

Проверено: два независимых мутационных прогона Codex по BASE-коду (отчёты
`/home/deploy/gitlab/9qw/tg-claude-userbot/tmp/antispam-stand/m2a-mutation-report.md` и
`.../m2b-mutation-report.md` — прочитай оба, там точные мутации и причины выживания). Живой стенд
(синтетический Bot API) прошёл 36 сценарных проверок: поведение кода верное, дыры — в тестах.
**Код вехи не меняется**: правки только в `*_test.go`. Если для теста нужен код (новый хук, экспорт) —
стоп по §6.

## 2. Какие тесты добавить (имена обязательны; один тест может быть табличным)

Все — в `internal/watch/` (реальный SQLite + `fake.Port`, как существующие тесты капчи), кроме отмеченных.
Каждый тест обязан падать на названной мутации (проверь: наложи мутацию руками, прогони, откати).

- `TestCaptchaPressExpiredByDeadlineWithoutSweep` — `Now` сдвинут до `Deadline` и за него, `Sweep` НЕ
  вызывается: нажатие → `expired`, строка `challenged`, `UnrestrictMember` не звался. (M14)
- `TestCaptchaPressSanctionChecks` — (а) санкция есть до нажатия: нет `MarkCaptchaPassing` (строка
  переходит `challenged→cancelled` напрямую, не через `passing`); (б) санкция появляется ПОСЛЕ первой
  проверки (обёртка стора вставляет инцидент в момент `MarkCaptchaPassing`): `cancelled_sanctioned`,
  `UnrestrictMember` не звался. (M15, M16)
- `TestCaptchaSweepChallengedSanctioned` — due `challenged` + санкция после `CreatedAt`, капча включена,
  не dry-run, для `kick` и `keep_muted`: `cancelled_sanctioned`, запрос удалён, ни ban/unban/unrestrict. (M20)
- `TestCaptchaGiveUpThresholds` — таблица: `failing` kick и unrestrict с `tries=5`; `passing` (button) с
  `tries=5`; `passing` (join_request) с `tries=5`; `failing` decline с `tries=5`: `gave_up`, конечное
  состояние, НИ ОДНОГО вызова Telegram-действия. Плюс `tries=4` — вызов есть. (M23, M56, J11, J32)
- `TestCaptchaSweepPassingSanctioned` — due `passing` (button) + санкция после `CreatedAt`: `cancelled`,
  `cancelled_sanctioned`, `UnrestrictMember` не звался. (M25)
- `TestCaptchaCallbackMalformedAttempt` — data с `attempt=0` и `-1`: `invalid`, строка не читается и не
  меняется (обёртка стора считает `GetCaptcha`). (M28)
- `TestCaptchaRestrictErrorCancels` — `RestrictErr`: после `Wait` строка `cancelled`, запрос не
  отправлен, метрика `error`; повторное вступление начинает попытку `attempt=2`. (M55)
- `TestJoinRequestSweepHonoursGateNow` — кнопка отправлена, затем выключили капчу чата (и отдельно
  включили dry-run через `force_dry_run`), `Sweep` после дедлайна: `left_pending`, `DeclineJoinRequest`
  не звался. (J9)
- `TestJoinRequestRetries` — (а) `ApproveErr` держится на первом `Sweep` по `passing`: `tries+1`,
  дедлайн перенесён, затем ошибка снята — следующий `Sweep` после нового дедлайна одобряет, `passed`;
  (б) то же для `failing`/`decline` с `DeclineErr`: доходит до `failed`. (J10, J31)
- `TestJoinRequestPressIgnoresSanction` — у заявителя есть санкционный инцидент после `CreatedAt`:
  нажатие всё равно одобряет заявку (`approved`), обёртка стора фиксирует, что `SanctionSince` не
  вызывался. (J15)
- `TestJoinRequestSweepOrphansAndFailing` — (а) осиротевшая due `new` с `mode=join_request` (не в полёте)
  → `cancelled`, `left_pending`, ни одного вызова Telegram (в т.ч. `UnrestrictMember`); (б) due `failing`
  с `decline` после «рестарта» (новый `Captcha` на той же БД) → `DeclineJoinRequest`, `failed`. (J29, J30)
- `internal/store`: `TestCaptchaRestartResetsPromptChat` — строка `join_request` с ненулевым
  `PromptChatID` в `cancelled`/`failed`, `BeginCaptcha` → `PromptChatID == 0`, `Mode` новый. (J19)
- `cmd/tg-antispam`: `TestChatJoinRequestRunsCaptcha` — `handleChatJoinRequest` с включённой капчей
  `join_request` (реальный `watch.Captcha` на временной БД, `fake.Port`): после выполнения задачи и
  `captcha.Wait()` кнопка ушла в `UserChatID`, метрика `challenged`. (J35)

## 3. Не трогать

Любые не-тестовые файлы. Существующие тесты не ослаблять и не удалять. Живой Telegram, прод, токены,
host tmux, сигналы чужим процессам.

## 4. Критерии приёмки

- **AC-001.** Полный сьют с гонками зелёный:
  `bash -c './scripts/dev.sh test -race -count=1 ./...'`
- **AC-002.** vet и gofmt:
  `bash -c './scripts/dev.sh vet ./... && test -z "$(docker run --rm -u "$(id -u):$(id -g)" -v "$PWD":/src -w /src golang:1.26.6 gofmt -l cmd internal)"'`
- **AC-003.** Новые тесты watch:
  `bash -c 'ts="TestCaptchaPressExpiredByDeadlineWithoutSweep TestCaptchaPressSanctionChecks TestCaptchaSweepChallengedSanctioned TestCaptchaGiveUpThresholds TestCaptchaSweepPassingSanctioned TestCaptchaCallbackMalformedAttempt TestCaptchaRestrictErrorCancels TestJoinRequestSweepHonoursGateNow TestJoinRequestRetries TestJoinRequestPressIgnoresSanction TestJoinRequestSweepOrphansAndFailing" && re=$(printf "%s|" $ts) && out=$(./scripts/dev.sh test -count=1 -v -run "^(${re%|})$" ./internal/watch/ 2>&1) && for t in $ts; do printf "%s\n" "$out" | grep -Eq -- "--- PASS: $t( |$)" || exit 1; done'`
- **AC-004.** Новые тесты store и cmd:
  `bash -c 'a=$(./scripts/dev.sh test -count=1 -v -run "^TestCaptchaRestartResetsPromptChat$" ./internal/store/ 2>&1) && b=$(./scripts/dev.sh test -count=1 -v -run "^TestChatJoinRequestRunsCaptcha$" ./cmd/tg-antispam/ 2>&1) && printf "%s\n" "$a" | grep -Eq -- "--- PASS: TestCaptchaRestartResetsPromptChat( |$)" && printf "%s\n" "$b" | grep -Eq -- "--- PASS: TestChatJoinRequestRunsCaptcha( |$)"'`
- **AC-005.** Только тесты, дерево чистое:
  `bash -c 'f=$(mktemp) && git diff --name-only 909796a3ad74edaba498d63240b14f86a75d355e..HEAD > "$f" && test -s "$f" && ! grep -qvE "^(cmd/tg-antispam|internal/(watch|store))/[a-z0-9_]+_test\.go$" "$f" && test -z "$(git status --porcelain -- . ":(exclude)report.json" ":(exclude)report-blocked.md" ":(exclude)tmp")"'`
- **AC-006.** Ревью-дифф влезает в потолок:
  `bash -c 'n=$(git diff 909796a3ad74edaba498d63240b14f86a75d355e..HEAD | wc -c) && test "$n" -gt 0 && test "$n" -le 70000'`

## 5. Контракт отчёта

`report.json` в корне клона, схемы v2, untracked:

```json
{"schema_version": 2,
 "policy_id": "cross-review-v1",
 "handoff_status": "ready",
 "executor": {"backend": "grok", "model": "<точная модель>"},
 "spec_sha256": "<sha256 файла /home/deploy/github/telegram-antispam/docs/specs/captcha-test-gaps.md>",
 "base_sha": "909796a3ad74edaba498d63240b14f86a75d355e",
 "reviewed_sha": "<коммит, ушедший ревьюеру>",
 "final_sha": "<HEAD клона>",
 "review": {"resolutions": [], "initial_receipts": [], "verification_receipts": []},
 "criteria": [{"id": "AC-001", "status": "pass|fail|blocked",
               "command": "<команда-доказательство>", "rc": 0, "note": "…"}]}
```

Ровно шесть записей AC-001…AC-006, `command` посимвольно, перезапуск на `final_sha`; `handoff_status` —
`ready|blocked|needs_owner`; у `blocked` `rc: null` и дословная ошибка. В `note` AC-003/AC-004 — для
каждого теста: какую мутацию ты накладывал руками и что тест на ней упал (имя мутации из §2).

## 6. Авторевью и контракт на невыполнимое

Ревьюер один — `codex` (`agy` без квоты до ~29.09; не запускать и не подменять):

```
bash /home/deploy/.claude/skills/executor-milestone/scripts/review_run.sh initial codex \
  --clone /home/deploy/exec-clones/antispam-captcha-tg-20260926 \
  --base 909796a3ad74edaba498d63240b14f86a75d355e \
  --range 909796a3ad74edaba498d63240b14f86a75d355e..<REVIEW_SHA> \
  --context "tg-antispam M2-tg: тесты против выживших мутантов капчи (без изменения кода); спека /home/deploy/github/telegram-antispam/docs/specs/captcha-test-gaps.md; только чтение"
```

Нужен вердикт `ВЕРДИКТ:`; временный сбой — один повтор; квота — `blocked`. Находки перепроверь, один
заход правок; `verify` (та же команда, `verify`, диапазон `<REVIEW_SHA>..<FINAL_SHA>`) — ТОЛЬКО если
правки были, иначе не запускать. После старта `verify` не коммитить. `review.resolutions` — как в
`captcha-m2a.md` §9 п.6.

Мутация эквивалентна (не убивается никаким тестом) — объясни почему в `note` AC-003/AC-004 и
продолжай. Тест невозможно написать без правки кода или противоречие со спекой — **стоп**: `report-blocked.md` с командой и выводом. Запрещено:
`|| true`, `--no-deps`, ослабление тестов, `git push`.
