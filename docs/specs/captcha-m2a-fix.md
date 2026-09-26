# Веха M2a-fix: заход исправлений по ревью капчи M2a

- Репозиторий `/home/deploy/github/telegram-antispam`, дата 26.09.2026.
- BASE_SHA: `ed10b6e835a798f81a67949b675a74416456ab9f` (финал M2a, ветка `captcha-m2a`). Клон снят ровно с
  BASE. Спека в клоне не лежит: читай по абсолютному пути
  `/home/deploy/github/telegram-antispam/docs/specs/captcha-m2a-fix.md`. Исходная спека вехи (контекст,
  §3.4–3.6 — действующий контракт, кроме пунктов ниже): `/home/deploy/github/telegram-antispam/docs/specs/captcha-m2a.md`.
- Исполнитель: `gk` (Grok). Это ЕДИНСТВЕННЫЙ заход исправлений; мутации — координатор (Codex).
- Критериев: 6.

## 0. Где работать

Клон `/home/deploy/exec-clones/antispam-captcha-m2a-fix-20260926`, ветка `captcha-m2a-fix`. Живое дерево
не трогать, push запрещён, `git add` только по именам. `.gopath/` постановщик положил. `tmp/` — для
улик, не коммитить. Прочитай `internal/watch/captcha.go`, `internal/store/captcha.go`,
`internal/watch/welcome.go` до первой правки.

## 1. Что проверено вживую, а что предположение: находки, которые чиним

Все пункты подтверждены постановщиком чтением кода BASE (номера строк — на BASE) и живым стендом
(синтетический Bot API): 25 сценарных проверок M2a прошли, дефекты ниже стендом не ловятся — это
гонки и сбои. Предположений нет.

- **F1. Старт капчи не попадает в метрику.** `OnJoin` при запуске проверки возвращает `""`
  (`captcha.go:242`), а `noteCaptcha` пустой исход не считает. Нужно: возвращать `challenged`
  (константа `CaptchaChallenged = "challenged"`), он считается в `tg_antispam_captcha_total`.
- **F2. `passed` записывается до `UnrestrictMember`.** Падение процесса между шагами (или ошибка
  отката, которая сейчас игнорируется) оставляет вечный мут, а повторный вход — `skip_known`.
  Нужно: новое состояние `passing`. Нажатие: `challenged→passing` (в той же записи `deadline=now`),
  повторная проверка `SanctionSince`, `UnrestrictMember`, `passing→passed`. Ошибка `UnrestrictMember`
  или стора — строка остаётся `passing`, ответ на callback «accepted, access will be restored
  shortly», исход `error`. `DueCaptchas` отдаёт и `passing`; `Sweep` для `passing`: `SanctionSince` →
  `cancelled` без снятия мута (`cancelled_sanctioned`); иначе `UnrestrictMember` → `passed`
  (`passed`), ошибка → `RetryCaptcha` (разрешить для `failing` И `passing`), при `tries >= 5` →
  `cancelled`, `gave_up`. `CaptchaPassed` — только `passed`; `BeginCaptcha` на `passing` → `false`.
- **F3. Снятие мута по `unrestrict` без повторной проверки санкций.** `fail()` для `unrestrict`
  (сирота `new`, сбой запроса, dry-run/выключение) и повторы через 60 с снимают мут, даже если
  модерация уже наказала вступившего. Нужно: перед КАЖДЫМ `UnrestrictMember` в `fail()` —
  `SanctionSince(row.CreatedAt)`; есть санкция → `failing→cancelled`, мут не трогать,
  `cancelled_sanctioned`.
- **F4. `SanctionSince` считает любые инциденты.** Инцидент без санкции (review/quarantine,
  `delete_only`, `none`) отменяет капчу и оставляет её мут навсегда. Нужно: учитывать только
  инциденты пары с `dry_run=0`, `created_at >= since` и строкой `audit` этого инцидента с
  `action IN ('delete_mute','mute','ban')` (audit пишется атомарно с инцидентом, `incidents.go:41`).
- **F5. Ошибка `SetCaptchaPrompt` оставляет `challenged` без кнопки** — по дедлайну человек получит
  `on_fail`. Нужно: ошибка `SetCaptchaPrompt` → удалить отправленный запрос и fail open:
  `FailCaptcha(challenged→failing, unrestrict)` и `fail`.
- **F6. Дедлайн отсчитывается от вступления, а не от показа кнопки.** Очередь Telegram (мут + запрос)
  съедает время на нажатие. Нужно: `SetCaptchaPrompt(chat, user, attempt, ephemeralID, messageID,
  deadline int64)` пишет и новый дедлайн; `challenge()` передаёт `now()+timeout` на момент после
  успешной отправки. Дедлайн при вставке остаётся (страховка на случай, если запрос не дошёл).
- **F7. Мут после отмены админом.** Админ изменил участника, пока `RestrictMember` ещё выполнялся →
  строка `cancelled`, а наш поздний мут остаётся без задания. Что хотел админ, бот знать не может,
  поэтому состояние не трогаем, но делаем видимым: если после успешного `RestrictMember` переход
  `new→challenged` не прошёл и строка `cancelled` — лог с chat/user и исход `restrict_after_cancel`
  в метрику.
- **F8. Двойное приветствие.** `Deliver` снимает резерв (`finish`) до `MarkWelcomed`; повторное
  вступление в этот промежуток проходит `WasWelcomed=false` и не видит резерва. Нужно: снимать
  резерв ПОСЛЕ `MarkWelcomed` (время для окна потолка — по-прежнему момент завершения отправки).
- **F9. Явные нули в конфиге подменяются умолчаниями до `Validate`.** `captcha.timeout: 0s`, пустые
  `text`/`button_text` молча становятся умолчаниями. Нужно: в `Captcha` поля `Timeout *Duration`,
  `Text *string`, `ButtonText *string` (nil = не задано → умолчание); явный `0s`/пустая строка —
  отказ `Validate` с именем ключа. Записи чата — как сейчас (пусто/0 = наследовать).

## 2. Не трогать

Всё из §4 исходной спеки. Плюс: не менять поведение, не названное в §1; не удалять и не ослаблять
существующие тесты — менять их можно только там, где §1 меняет контракт (сигнатура
`SetCaptchaPrompt`, `challenged` вместо `""`, `passing`), и в каждом таком тесте сохранить проверку.

## 3. Тесты (имена обязательны)

- `internal/watch`: `TestCaptchaChallengedCounted`, `TestCaptchaPassingSurvivesCrash` (строка
  `passing` в БД, новый `Captcha` → `Sweep` снимает мут и пишет `passed`),
  `TestCaptchaPressUnrestrictErrorKeepsPassing` (ошибка → `passing`, следующий `Sweep` доводит),
  `TestCaptchaFailOpenRespectsSanction` (санкция после старта → `fail(unrestrict)` мут не снимает),
  `TestCaptchaPromptSaveErrorFailsOpen`, `TestCaptchaDeadlineStartsAtPrompt` (`Now` сдвинут между
  вставкой и отправкой — дедлайн = время отправки + timeout), `TestCaptchaRestrictAfterCancelCounted`,
  `TestWelcomerNoDuplicateWhileMarking` (`MarkWelcomed` блокирован — второй `Admit` той же пары →
  `skip_known`).
- `internal/store`: `TestSanctionSinceOnlyMuteOrBan` (по одному инциденту на `delete_mute`, `mute`,
  `ban` → true; `quarantine`, `delete_only`, `none`, `dry_run=1`, более ранний — false).
- `internal/config`: `TestCaptchaValidateExplicitZeroAndEmpty`.

## 4. Критерии приёмки

- **AC-001.** Полный сьют с гонками зелёный:
  `bash -c './scripts/dev.sh test -race -count=1 ./...'`
- **AC-002.** vet, сборка и gofmt чистые:
  `bash -c './scripts/dev.sh vet ./... && ./scripts/dev.sh build ./... && test -z "$(docker run --rm -u "$(id -u):$(id -g)" -v "$PWD":/src -w /src golang:1.26.6 gofmt -l cmd internal)"'`
- **AC-003.** Новые тесты watch:
  `bash -c 'out=$(./scripts/dev.sh test -count=1 -v -run "^(TestCaptchaChallengedCounted|TestCaptchaPassingSurvivesCrash|TestCaptchaPressUnrestrictErrorKeepsPassing|TestCaptchaFailOpenRespectsSanction|TestCaptchaPromptSaveErrorFailsOpen|TestCaptchaDeadlineStartsAtPrompt|TestCaptchaRestrictAfterCancelCounted|TestWelcomerNoDuplicateWhileMarking)$" ./internal/watch/ 2>&1) && for t in TestCaptchaChallengedCounted TestCaptchaPassingSurvivesCrash TestCaptchaPressUnrestrictErrorKeepsPassing TestCaptchaFailOpenRespectsSanction TestCaptchaPromptSaveErrorFailsOpen TestCaptchaDeadlineStartsAtPrompt TestCaptchaRestrictAfterCancelCounted TestWelcomerNoDuplicateWhileMarking; do printf "%s\n" "$out" | grep -Eq -- "--- PASS: $t( |$)" || exit 1; done'`
- **AC-004.** Новые тесты store и config:
  `bash -c 'a=$(./scripts/dev.sh test -count=1 -v -run "^TestSanctionSinceOnlyMuteOrBan$" ./internal/store/ 2>&1) && b=$(./scripts/dev.sh test -count=1 -v -run "^TestCaptchaValidateExplicitZeroAndEmpty$" ./internal/config/ 2>&1) && printf "%s\n" "$a" | grep -Eq -- "--- PASS: TestSanctionSinceOnlyMuteOrBan( |$)" && printf "%s\n" "$b" | grep -Eq -- "--- PASS: TestCaptchaValidateExplicitZeroAndEmpty( |$)"'`
- **AC-005.** Состав работы в границах, дерево чистое:
  `bash -c 'f=$(mktemp) && git diff --name-only ed10b6e835a798f81a67949b675a74416456ab9f..HEAD > "$f" && test -s "$f" && ! grep -qvE "^(CHANGELOG\.md|config\.example\.yaml|docs/architecture\.md|cmd/tg-antispam/[a-z0-9_]+\.go|internal/(telegram|telegram/fake|config|store|watch)/[a-z0-9_]+\.go)$" "$f" && test -z "$(git status --porcelain -- . ":(exclude)report.json" ":(exclude)report-blocked.md" ":(exclude)tmp")"'`
- **AC-006.** Ревью-дифф влезает в потолок:
  `bash -c 'n=$(git diff ed10b6e835a798f81a67949b675a74416456ab9f..HEAD | wc -c) && test "$n" -gt 0 && test "$n" -le 60000'`

## 5. Контракт отчёта

`report.json` в корне клона, схемы v2, untracked:

```json
{"schema_version": 2,
 "policy_id": "cross-review-v1",
 "handoff_status": "ready",
 "executor": {"backend": "grok", "model": "<точная модель>"},
 "spec_sha256": "<sha256 файла /home/deploy/github/telegram-antispam/docs/specs/captcha-m2a-fix.md>",
 "base_sha": "ed10b6e835a798f81a67949b675a74416456ab9f",
 "reviewed_sha": "<коммит, ушедший ревьюеру>",
 "final_sha": "<HEAD клона>",
 "review": {"resolutions": [], "initial_receipts": [], "verification_receipts": []},
 "criteria": [{"id": "AC-001", "status": "pass|fail|blocked",
               "command": "<команда-доказательство>", "rc": 0, "note": "…"}]}
```

Ровно шесть записей AC-001…AC-006, `command` посимвольно, каждая перезапущена на `final_sha`;
`handoff_status` — `ready|blocked|needs_owner`; у `blocked` `rc: null`; `resolutions` — как §9 п.6
исходной спеки.

## 6. Ревью

Перекрёстное ревью — §9 исходной спеки, но: клон и диапазон этой вехи
(`--clone /home/deploy/exec-clones/antispam-captcha-m2a-fix-20260926 --base ed10b6e835a798f81a67949b675a74416456ab9f --range ed10b6e835a798f81a67949b675a74416456ab9f..<REVIEW_SHA>`),
команда:

```
bash /home/deploy/.claude/skills/executor-milestone/scripts/review_run.sh initial codex \
  --clone /home/deploy/exec-clones/antispam-captcha-m2a-fix-20260926 \
  --base ed10b6e835a798f81a67949b675a74416456ab9f \
  --range ed10b6e835a798f81a67949b675a74416456ab9f..<REVIEW_SHA> \
  --context "tg-antispam M2a-fix: заход исправлений F1–F9 по ревью капчи; спека /home/deploy/github/telegram-antispam/docs/specs/captcha-m2a-fix.md; только чтение"
```

(для `verify` — `verify` вместо `initial` и диапазон `<REVIEW_SHA>..<FINAL_SHA>`), контекст «tg-antispam M2a-fix: заход исправлений F1–F9 по ревью капчи; спека
/home/deploy/github/telegram-antispam/docs/specs/captcha-m2a-fix.md; только чтение». Ревьюер `agy` сейчас
без квоты (RESOURCE_EXHAUSTED до ~29.09): **в этой вехе ревьюер один — `codex`**, одна квитанция на
фазу допустима; `agy` не запускать. Правки по ревью — один заход, `verify` только если были правки;
после старта `verify` ничего не коммитить.

## 7. Контракт на невыполнимое

Как §8 исходной спеки: противоречие, опровергнутый факт §1, нужда трогать пути вне AC-005 или живой
Telegram — стоп, `report-blocked.md` с дословной командой и выводом. Обходы (`|| true`, `--no-deps`,
ослабление тестов, `git push`) запрещены.
