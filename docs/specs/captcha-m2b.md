# Веха M2b: капча по заявке на вступление (`mode: join_request`)

- Репозиторий `/home/deploy/github/telegram-antispam`, дата 26.09.2026.
- BASE_SHA: `a10dd9c352d21edaa46a3dd9bb540551eb27f648` (финал M2a-fix, ветка `captcha-m2a-fix`). Клон снят
  ровно с BASE. Спека в клоне не лежит: читай её по абсолютному пути
  `/home/deploy/github/telegram-antispam/docs/specs/captcha-m2b.md`. Контекст M2a (действующий контракт
  капчи-кнопки): `docs/specs/captcha-m2a.md` и `docs/specs/captcha-m2a-fix.md` там же.
- Исполнитель: `gk` (Grok). Исправления по ревью — тот же исполнитель, один заход.
- Критериев: 10.
- Решение владельца 24.09.2026 (`docs/spec-queue.md`): `mode: join_request` (только чаты со вступлением
  по заявке): `chat_join_request` в `allowed_updates`; в личку заявителю (`user_chat_id`, окно 5 минут)
  кнопка, нажал — `approveChatJoinRequest`, таймаут — `declineChatJoinRequest`. Join Request Queries
  (Bot API 10.1) — НЕ используем.

## 0. Где работать

Клон `/home/deploy/exec-clones/antispam-captcha-m2b-20260926`, ветка `captcha-m2b`. Живое дерево не трогать,
push запрещён, `git add` только по именам, никогда `-A`. `.gopath/` постановщик положил. `tmp/` — для
улик ревью, не коммитить. До первой правки прочитай `AGENTS.md`, `docs/architecture.md` (раздел про
капчу), `internal/watch/captcha.go`, `internal/store/captcha.go`, `cmd/tg-antispam/chatmember.go`.

## 1. Задача

Чат, где вступление идёт по заявке, получает капчу без мута: бот ловит `chat_join_request`, шлёт
заявителю в личку кнопку; нажал сам — заявка одобрена; не нажал до дедлайна — `on_fail`. Та же таблица
`captcha_challenges`, те же `attempt`, `Sweep`, маршрут callback'ов `cap:` и отмена действием админа.

## 2. Что проверено постановщиком 26.09.2026, а что предположение

Проверено чтением кода BASE и `go-telegram/bot@v1.27.0` в `.gopath`:

- **П1.** `models.Update.ChatJoinRequest *models.ChatJoinRequest`; `models.ChatJoinRequest{Chat, From
  models.User, UserChatID int64, Date int, Bio string, InviteLink *ChatInviteLink}`;
  `models.AllowedUpdateChatJoinRequest = "chat_join_request"`. `bot.ApproveChatJoinRequest(ctx,
  &bot.ApproveChatJoinRequestParams{ChatID any, UserID int64})`, `bot.DeclineChatJoinRequest(ctx,
  &bot.DeclineChatJoinRequestParams{ChatID any, UserID int64})`, оба `(bool, error)`.
- **П2.** `allowed_updates` — литерал в `cmd/tg-antispam/main.go` (`tgbot.WithAllowedUpdates([]string{…})`),
  `chat_join_request` в нём нет. Ветки `update.ChatJoinRequest` в обработчике нет.
- **П3.** `LivePort.SendCaptchaMessage(ctx, chat, text, buttons)` — обычный `sendMessage` с клавиатурой в
  ЛЮБОЙ чат, в том числе личный (`user_chat_id`); `DeleteMessages(ctx, chat, ids)`.
- **П4.** Капча M2a: `config.Captcha.For` → `CaptchaPolicy{Mode, Timeout, OnFail, Text, ButtonText}`;
  `Validate` сейчас отвергает любой `mode`, кроме `button` (текст `not supported`). Стор:
  `BeginCaptcha`, `TransitionCaptcha`, `MarkCaptchaPassing`, `FailCaptcha`, `RetryCaptcha`,
  `SetCaptchaPrompt(…, ephemeralID, messageID, deadline)` (дедлайн пишется только в `challenged`),
  `DueCaptchas` (`new|challenged|failing|passing`), `SanctionSince`. Колонок режима и чата запроса в
  таблице нет. Миграция колонок — `addColumnIfMissing` в `internal/store/migrate.go`.
- **П5.** `watch.Captcha`: `gate` (допуск, выключатель, строка чата, dry-run), `OnJoin`, `OnMemberChange`
  (отмена, если участника изменил не бот и не он сам), `decidePress` (`challenged→passing→passed`,
  `UnrestrictMember`), `Sweep` (`sweepChallenged`/`sweepPassing`/`fail`), `deleteIDs` (удаляет
  `MessageID` в ЧАТЕ ГРУППЫ). `fail_action`: `unrestrict|kick|keep_muted`.

Предположения (живого Telegram нет; проверит координатор на стенде):

- **П6.** Бот может написать заявителю в `user_chat_id` в течение 5 минут после заявки, даже если тот
  не запускал бота; нажатие кнопки в личке приходит обычным `callback_query` с `From` = заявитель.
- **П7.** После `approveChatJoinRequest` приходит `chat_member` (left→member) с `via_join_request=true`;
  если заявку одобрил админ вручную, `from` этого `chat_member` — админ.

## 3. Что сделать

### 3.1 Конфиг

- `Validate` принимает `mode: join_request` (общий и в записи чата); прочие значения, кроме `button` и
  `join_request`, — отказ с именем ключа.
- Смысл `on_fail` для `join_request`: `kick` → отклонить заявку (`declineChatJoinRequest`);
  `keep_muted` → оставить заявку висеть на решение админов (бот её не трогает). Записать это в
  `config.example.yaml`.

### 3.2 `internal/telegram`

- `type JoinRequest struct{ ChatID, UserID, UserChatID int64; DisplayName string }` и
  `func JoinRequestFromUpdate(r models.ChatJoinRequest) (JoinRequest, bool)`: `false` для бота, нулевого
  `From.ID` или нулевого `UserChatID`; `DisplayName` = `From.FirstName`.
- Порт: `ApproveJoinRequest(ctx, chat, user int64) error`, `DeclineJoinRequest(ctx, chat, user int64) error`
  через диспетчер, приоритет по умолчанию. Fake: `ApproveErr`, `DeclineErr`, `LastApprove`/`LastDecline
  {Chat, User}`, запись в журнал вызовов.

### 3.3 Стор

- Колонки `mode TEXT NOT NULL DEFAULT 'button'` и `prompt_chat_id INTEGER NOT NULL DEFAULT 0` в
  `captcha_challenges` — и в `CREATE TABLE`, и через `addColumnIfMissing` (база, созданная кодом M2a,
  должна мигрировать). `CaptchaRow` получает `Mode string`, `PromptChatID int64`.
- `BeginCaptcha` получает режим: `BeginCaptcha(chat, user, now, deadline int64, mode string)`; при
  перезапуске строки режим перезаписывается. Вызов из `OnJoin` передаёт `"button"`.
- `SetCaptchaPrompt` получает чат запроса: `SetCaptchaPrompt(chat, user, attempt int64, promptChatID
  int64, ephemeralID, messageID int, deadline int64)`; `promptChatID=0` — чат группы (как в M2a).

### 3.4 `watch.Captcha` — новый файл `internal/watch/captcha_join_request.go`

Исходы (метка `tg_antispam_captcha_total`): `skip_mode` (режим чата не тот), `approved_known`,
`prompt_failed`, `approved` (нажал), `failed_decline`, `left_pending`.

**`OnJoinRequest(ctx, r telegram.JoinRequest) (CaptchaOutcome, error)`** — решение в задаче чата, сеть в
`Go`:

1. `gate` (допуск, выключатель, строка чата, dry-run) → те же `skip_*`, заявку не трогать;
2. режим политики чата не `join_request` → `skip_mode`, заявку не трогать (решают админы);
3. блоклист `Listed` → `skip_blocklisted`, заявку не трогать;
4. `CaptchaPassed` или `TrustCount > 0` → в `Go` `ApproveJoinRequest`, исход `approved_known` (ошибка —
   лог и `error`; строка не создаётся);
5. `BeginCaptcha(…, "join_request")` → `false` → `skip_pending`;
6. иначе `challenged`, и в `Go`: `TransitionCaptcha(new→challenged)`; `SendCaptchaMessage(r.UserChatID,
   text, кнопка)` (без имени в тексте — это личка); ошибка → `TransitionCaptcha(challenged→cancelled)`,
   исход `prompt_failed`, заявку не трогать; успех → `SetCaptchaPrompt(promptChatID=r.UserChatID,
   messageID, deadline = now+timeout)`; строка уже не `challenged` → удалить отправленное.

**Нажатие** — существующий `decidePress`, ветвление по `row.Mode`: для `join_request` вместо
`UnrestrictMember` — `ApproveJoinRequest`; `SanctionSince` для `join_request` не проверять (человек ещё
не в чате); успех → `passed`, исход `approved`; ошибка → строка остаётся `passing`, `Sweep` повторяет
`ApproveJoinRequest` (для `join_request` в `sweepPassing` тоже без `SanctionSince`).

**`Sweep`** для строк `mode=join_request`:

- `new` не в полёте → `cancelled` без вызовов Telegram, `left_pending` (fail open = оставить админам);
- `challenged`: `gate` не пропускает (выключили, dry-run, чат не допущен) → `cancelled`, `left_pending`;
  иначе по `on_fail`: `kick` → `FailCaptcha(→failing, "decline")`, `DeclineJoinRequest` → `failed`,
  `failed_decline`; `keep_muted` → `cancelled`, `left_pending`. Запрос в личке удалить;
- `failing` с `decline` → повтор `DeclineJoinRequest` (та же механика `tries`/`gave_up`);
- `fail()` получает действие `decline`.

**`deleteIDs`** удаляет `MessageID` в `PromptChatID`, если он не 0, иначе в чате группы (сигнатуру
поменять под это; все вызовы передают строку/чат запроса).

**`OnJoin`**: если режим политики чата `join_request` — `skip_mode` (проверка была на заявке; вступление
без заявки добавил админ). `OnMemberChange` без изменений: одобрение заявки админом (`from` = админ)
отменяет строку `challenged` и удаляет кнопку в личке.

### 3.5 `main.go`

- `"chat_join_request"` в `allowed_updates` — вынести список в переменную/функцию пакета `main`, чтобы
  тест мог его проверить.
- Ветка `update.ChatJoinRequest != nil`: счётчик `tg_antispam_updates_total{kind="chat_join_request"}`,
  `JoinRequestFromUpdate`, `seq.Submit(chatID, …)` → `captcha.OnJoinRequest` и метрика. Тело — функция
  пакета `main` (`handleChatJoinRequest`) для теста.

### 3.6 Документация

`config.example.yaml` (режим `join_request`, смысл `on_fail`, нужное право бота «добавление
участников» — `can_invite_users`, работает только в чатах со вступлением по заявке), `CHANGELOG.md`
(Unreleased, Added), `docs/architecture.md` (абзац про путь заявки).

### 3.7 Тесты (имена обязательны)

- `internal/config`: `TestCaptchaJoinRequestModeAccepted` (общий и в записи; `mode: foo` — отказ с ключом).
- `internal/telegram`: `TestJoinRequestFromUpdate` (таблица: обычный, бот, нулевой id, нулевой
  `user_chat_id`), `TestApproveDeclineJoinRequestParams` (httptest: метод и `chat_id`/`user_id`).
- `internal/store`: `TestCaptchaModeAndPromptChat` (режим и чат запроса пишутся/читаются, перезапуск
  меняет режим; `Migrate` на таблице в форме M2a без новых колонок добавляет их, данные целы).
- `internal/watch`: `TestJoinRequestSkips` (шаги 1–3 и 5: исход и отсутствие вызовов Telegram),
  `TestJoinRequestPromptsInPrivate` (сообщение в `UserChatID`, data с `attempt`, строка `challenged`,
  `PromptChatID`), `TestJoinRequestPressApproves` (одобрение, `passed`, удаление кнопки в личке),
  `TestJoinRequestWrongUserRejected`, `TestJoinRequestTimeoutDeclines`,
  `TestJoinRequestKeepMutedLeavesPending` (ни approve, ни decline), `TestJoinRequestPromptFailureLeavesForAdmins`,
  `TestJoinRequestKnownApproved`, `TestJoinRequestApproveErrorRetried` (ошибка approve → `passing`,
  `Sweep` доводит), `TestJoinRequestAdminApproveCancels`, `TestJoinInJoinRequestChatSkipsButton`.
- `cmd/tg-antispam`: `TestAllowedUpdatesIncludeJoinRequest`, `TestChatJoinRequestRouting`.

## 4. Не трогать

- `internal/incident`, `internal/admin`, `internal/detect`, `internal/queue`, `internal/blocklist`,
  `internal/llm`, `internal/ops`, `internal/selfcheck`, `internal/train`, `internal/domain`.
- Поведение капчи-кнопки M2a (кроме подписи `BeginCaptcha`/`SetCaptchaPrompt`/`deleteIDs`) и её тесты:
  не ослаблять, не удалять; поправить вызовы под новые сигнатуры можно.
- `.github/`, `Dockerfile`, `Makefile`, `scripts/`, `go.mod`, `go.sum`, `deploy/helm/tg-antispam/Chart.yaml`,
  `deploy/helm/tg-antispam/templates/`, `docs/specs/`, `docs/spec-queue.md`.
- Join Request Queries (`query_id`, `answerChatJoinRequestQuery`, `sendChatJoinRequestWebApp`), сырые
  HTTP-вызовы мимо библиотеки. Живой Telegram, прод, токены. Host tmux, сигналы чужим процессам.

## 5. Разрешения

Сеть — только модульный прокси через `./scripts/dev.sh` (новых модулей нет). Docker — `./scripts/dev.sh`
и `golang:1.26.6` для `gofmt`. Новые файлы — только в путях AC-009. Дифф `BASE..HEAD` ≤ 80000 байт (AC-010).

## 6. Критерии приёмки

- **AC-001.** Полный сьют с гонками зелёный:
  `bash -c './scripts/dev.sh test -race -count=1 ./...'`
- **AC-002.** vet, сборка и gofmt чистые:
  `bash -c './scripts/dev.sh vet ./... && ./scripts/dev.sh build ./... && test -z "$(docker run --rm -u "$(id -u):$(id -g)" -v "$PWD":/src -w /src golang:1.26.6 gofmt -l cmd internal)"'`
- **AC-003.** Конфиг и классификация заявки:
  `bash -c 'a=$(./scripts/dev.sh test -count=1 -v -run "^TestCaptchaJoinRequestModeAccepted$" ./internal/config/ 2>&1) && b=$(./scripts/dev.sh test -count=1 -v -run "^(TestJoinRequestFromUpdate|TestApproveDeclineJoinRequestParams)$" ./internal/telegram/ 2>&1) && printf "%s\n" "$a" | grep -Eq -- "--- PASS: TestCaptchaJoinRequestModeAccepted( |$)" && for t in TestJoinRequestFromUpdate TestApproveDeclineJoinRequestParams; do printf "%s\n" "$b" | grep -Eq -- "--- PASS: $t( |$)" || exit 1; done'`
- **AC-004.** Стор:
  `bash -c 'out=$(./scripts/dev.sh test -count=1 -v -run "^TestCaptchaModeAndPromptChat$" ./internal/store/ 2>&1) && printf "%s\n" "$out" | grep -Eq -- "--- PASS: TestCaptchaModeAndPromptChat( |$)"'`
- **AC-005.** Решения по заявке:
  `bash -c 'out=$(./scripts/dev.sh test -count=1 -v -run "^(TestJoinRequestSkips|TestJoinRequestPromptsInPrivate|TestJoinRequestPressApproves|TestJoinRequestWrongUserRejected|TestJoinRequestTimeoutDeclines|TestJoinRequestKeepMutedLeavesPending|TestJoinRequestPromptFailureLeavesForAdmins|TestJoinRequestKnownApproved|TestJoinRequestApproveErrorRetried|TestJoinRequestAdminApproveCancels|TestJoinInJoinRequestChatSkipsButton)$" ./internal/watch/ 2>&1) && for t in TestJoinRequestSkips TestJoinRequestPromptsInPrivate TestJoinRequestPressApproves TestJoinRequestWrongUserRejected TestJoinRequestTimeoutDeclines TestJoinRequestKeepMutedLeavesPending TestJoinRequestPromptFailureLeavesForAdmins TestJoinRequestKnownApproved TestJoinRequestApproveErrorRetried TestJoinRequestAdminApproveCancels TestJoinInJoinRequestChatSkipsButton; do printf "%s\n" "$out" | grep -Eq -- "--- PASS: $t( |$)" || exit 1; done'`
- **AC-006.** Подключение в `main`:
  `bash -c 'out=$(./scripts/dev.sh test -count=1 -v -run "^(TestAllowedUpdatesIncludeJoinRequest|TestChatJoinRequestRouting)$" ./cmd/tg-antispam/ 2>&1) && for t in TestAllowedUpdatesIncludeJoinRequest TestChatJoinRequestRouting; do printf "%s\n" "$out" | grep -Eq -- "--- PASS: $t( |$)" || exit 1; done'`
- **AC-007.** Тесты капчи-кнопки M2a целы:
  `bash -c 'out=$(./scripts/dev.sh test -count=1 -v -run "^TestCaptcha" ./internal/watch/ 2>&1) && n=$(printf "%s\n" "$out" | grep -Ec -- "^--- PASS: TestCaptcha") && test "$n" -ge 23 && ! printf "%s\n" "$out" | grep -q -- "--- FAIL"'`
- **AC-008.** Документация:
  `bash -c 'grep -q "join_request" config.example.yaml && grep -qi "can_invite_users" config.example.yaml && sed -n "/^## \[Unreleased\]/,/^## \[0/p" CHANGELOG.md | grep -qi "join request\|join_request" && grep -qi "join_request\|join request" docs/architecture.md'`
- **AC-009.** Состав работы в границах, дерево чистое:
  `bash -c 'f=$(mktemp) && git diff --name-only a10dd9c352d21edaa46a3dd9bb540551eb27f648..HEAD > "$f" && test -s "$f" && ! grep -qvE "^(CHANGELOG\.md|config\.example\.yaml|docs/architecture\.md|cmd/tg-antispam/[a-z0-9_]+\.go|internal/(telegram|telegram/fake|config|store|watch)/[a-z0-9_]+\.go)$" "$f" && test -z "$(git status --porcelain -- . ":(exclude)report.json" ":(exclude)report-blocked.md" ":(exclude)tmp")"'`
- **AC-010.** Ревью-дифф влезает в потолок:
  `bash -c 'n=$(git diff a10dd9c352d21edaa46a3dd9bb540551eb27f648..HEAD | wc -c) && test "$n" -gt 0 && test "$n" -le 80000'`

## 7. Контракт отчёта

`report.json` в корне клона, схемы v2, untracked:

```json
{"schema_version": 2,
 "policy_id": "cross-review-v1",
 "handoff_status": "ready",
 "executor": {"backend": "grok", "model": "<точная модель>"},
 "spec_sha256": "<sha256 файла /home/deploy/github/telegram-antispam/docs/specs/captcha-m2b.md>",
 "base_sha": "a10dd9c352d21edaa46a3dd9bb540551eb27f648",
 "reviewed_sha": "<коммит, ушедший ревьюеру>",
 "final_sha": "<HEAD клона>",
 "review": {"resolutions": [], "initial_receipts": [], "verification_receipts": []},
 "criteria": [{"id": "AC-001", "status": "pass|fail|blocked",
               "command": "<команда-доказательство>", "rc": 0, "note": "…"}]}
```

Ровно десять записей AC-001…AC-010, `command` посимвольно, каждая перезапущена на `final_sha`;
`handoff_status` — `ready|blocked|needs_owner`; у `blocked` `rc: null` и дословная ошибка в `note`;
цепочка `base_sha → reviewed_sha → final_sha` от предка к потомку.

## 8. Контракт на невыполнимое

Спека противоречит себе, факт §2 опровергнут, нужно трогать пути §4 или живой Telegram — **стоп**:
`report-blocked.md` с дословной командой, выводом и пунктом. Запрещено: `--no-deps`, `GOTOOLCHAIN=local`,
`|| true`, `set +e` в критерии, `sudo`, `git push`, ослабление/удаление чужих тестов, `replace` в `go.mod`.
Дифф не влезает в 80000 байт без потери обязательного — стоп, доложи `git diff --stat`.

## 9. Авторевью — обязательная часть вехи

Отчёт объявляет `policy_id: cross-review-v1`. Ревьюер `agy` без квоты до ~29.09, поэтому **ревьюер
один — `codex`**, одна квитанция на фазу; `agy` не запускать, другим ревьюером не подменять.

1. Реализуй, прогони критерии, закоммить. `REVIEW_SHA` = `HEAD` после последнего рабочего коммита.
2. Ревью:

   ```
   bash /home/deploy/.claude/skills/executor-milestone/scripts/review_run.sh initial codex \
     --clone /home/deploy/exec-clones/antispam-captcha-m2b-20260926 \
     --base a10dd9c352d21edaa46a3dd9bb540551eb27f648 \
     --range a10dd9c352d21edaa46a3dd9bb540551eb27f648..<REVIEW_SHA> \
     --context "tg-antispam M2b: капча по заявке на вступление (chat_join_request, кнопка в личку, approve/decline, on_fail для заявки); спека /home/deploy/github/telegram-antispam/docs/specs/captcha-m2b.md; только чтение"
   ```

3. `rc=0` ревью не доказывает: нужен вердикт (`ВЕРДИКТ:` `ПРИНЯТО`/`НЕ ПРИНИМАТЬ`). Временный сбой —
   один повтор; квота — `blocked`.
4. Каждую находку перепроверь по коду; подтверждённые исправь, критерии заново, закоммить. Заход ОДИН.
5. **`verify` — ТОЛЬКО если были правки** (`final_sha != reviewed_sha`): та же команда с `verify` и
   диапазоном `<REVIEW_SHA>..<FINAL_SHA>`. Правок не было — `verify` не запускать, `verification_receipts`
   пуст. После старта `verify` ничего не коммитить.
6. `review.resolutions` — по записи на находку (`finding_id`, `decision` из `fixed|disproved|needs_owner`,
   `source_receipt`, `proof`, `fix_commits` при `fixed`).

## 10. Стыки

Координатор после приёмки: мутации (Codex), стенд с синтетическим Bot API (заявка → личка → нажатие →
approve; таймаут → decline; `keep_muted` → заявка висит), релиз 0.18.0 вместе с M2a.
