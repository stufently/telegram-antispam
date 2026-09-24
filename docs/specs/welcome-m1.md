# Веха M1: приветствие новичку эфемерным сообщением (опция на чат)

- Репозиторий `/home/deploy/github/telegram-antispam` (публичный `stufently/telegram-antispam`), дата 24.09.2026.
- BASE_SHA: `067cb086ceccde87bc8208a39ff63a4eab26bdd5` («Address review of manual override», релиз 0.17.1).
  Клон снимается с коммита спеки, который лежит сразу над BASE и добавляет только
  `docs/specs/welcome-m1.md` и `docs/spec-queue.md`. BASE — левая граница ревью-диффа и
  значение `base_sha` в отчёте.
- Исполнитель: `auto` (Grok или Spark, выбор лаунчера). Исправления по ревью — тот же исполнитель.
  Мутации по новым тестам гоняет противоположный исполнитель (это делает координатор, не ты).
- Критериев: 10.
- Решение владельца 24.09.2026: приветствие и капча — опциями, всё выключено по умолчанию.
  Эта веха — только приветствие. Капча — следующая веха (M2, `docs/spec-queue.md`).

## 0. Где работать

Одноразовый клон `/home/deploy/exec-clones/antispam-welcome-m1-20260924`, новая ветка `welcome-m1`.
Живое дерево `/home/deploy/github/telegram-antispam` не трогать. **Push в origin запрещён: работу
заберёт постановщик через `git fetch` из клона.** Коммиты локальные, `git add` только по именам
файлов, никогда `-A`. Прочитай `AGENTS.md` и `docs/architecture.md` до первой правки.

### Что кладёт постановщик до запуска

| Что | Как | Коммитить? |
|---|---|---|
| эта спека и `docs/spec-queue.md` | уже в коммите клона | уже закоммичены, текст не менять |
| кэш модулей `.gopath/` | `./scripts/dev.sh mod download` в клоне | нет, `.gopath/` в `.gitignore` |

Сеть для `go get`/`go mod` разрешена только через `./scripts/dev.sh` (он уже ходит с `--network host`
и прокси из окружения). Каталог `tmp/` в клоне — для улик ревью, не коммитить.

## 1. Задача и почему

С 25.08.2026 Telegram даёт владельцам групп «приветственные сообщения», но задаются они только
в клиенте: в Bot API есть лишь право администратора `can_send_welcome_messages` (Bot API 10.3),
метода «задать приветствие» нет. Зато с Bot API 10.2 бот умеет **эфемерные сообщения** — видит
только адресат, доставка не гарантирована. Этим и делаем приветствие: вступившему в чат человеку
бот шлёт эфемерное сообщение с правилами чата и контактом, куда писать, если его ошибочно заглушили.
Опция на чат, по умолчанию выключена, текст задаётся в конфиге (боевые values — у координатора).

Попутно и обязательно в этой же вехе: **обновить `github.com/go-telegram/bot` с v1.23.0 до v1.27.0.**
Bot API 10.3 (24.08.2026) заменил параметры `receiver_user_id`/`callback_query_id` методов отправки
на объект `ephemeral_message_parameters`. Наш `LivePort.SendEphemeral` шлёт старый
`receiver_user_id` (v1.23.0 другого не умеет). Если Telegram старый параметр больше не читает,
то «личный» ответ модератору на `/spam` и уведомление `detection.ephemeral_notice_*` уходят
**в общий чат всем**. Вживую это не проверено (см. §2, П5) — поэтому, помимо перехода на новый
параметр, добавляем предохранитель: ответ без `ephemeral_message_id` считается публичным и тут же
удаляется.

Что сломается и чинится здесь же: v1.24.0 удалил поля `ReceiverUserID`/`CallbackQueryID` из
параметров отправки — `internal/telegram/livept.go` перестанет компилироваться, пока
`SendEphemeral` не переведён на `EphemeralMessageParameters`.

## 2. Что проверено постановщиком 24.09.2026, а что предположение

Проверено чтением кода на BASE и первичных источников:

- **П1.** `go.mod`: `github.com/go-telegram/bot v1.23.0`, `toolchain go1.26.6`. Последняя версия
  библиотеки по `proxy.golang.org/.../@latest` — **v1.27.0** (11.09.2026), README: «Supports Bot API
  10.3». CHANGELOG v1.24.0: `[BREAKING] ReceiverUserID and CallbackQueryID are removed from the send
  method params ... replaced them with EphemeralMessageParameters`; там же `can_send_welcome_messages`
  в `ChatAdministratorRights`/`ChatMemberAdministrator`/`promoteChatMember`. Ещё два BREAKING v1.24.0
  (`Message.ReplyToStore` → `ReplyToStory`, `BusinessBotRights.CanDeleteOutgoingMessages`) нас не
  задевают: `grep -rn 'ReplyToStore\|CanDeleteOutgoingMessages' cmd internal` пусто.
- **П2.** В v1.27.0: `models.EphemeralMessageParameters{ReceiverUserID int64; CallbackQueryID string;
  ReplaceCallbackQueryMessage bool}`, поле `SendMessageParams.EphemeralMessageParameters
  *models.EphemeralMessageParameters` (json `ephemeral_message_parameters`). `Message.EphemeralMessageID`
  есть с v1.23.0.
- **П3.** `LivePort.SendEphemeral` (`internal/telegram/livept.go:635`) шлёт
  `SendMessageParams{ChatID, ReceiverUserID, Text}` и возвращает `msg.EphemeralMessageID`. Зовут его
  `admin.Commands.notify` (`internal/admin/commands.go:402`) и `incident.Machine` (`machine.go:275`, `:405`).
- **П4.** `chat_member` уже в `WithAllowedUpdates` (`cmd/tg-antispam/main.go:387-390`). Ветка
  `update.ChatMember` (`main.go:434-458`) инвалидирует кэш админов при смене админского статуса и
  отправляет в секвенсор чата `memberWatcher.Observe` (запись имени, `watch.MemberEvent`).
  `models.ChatMemberUpdated` несёт `OldChatMember`, `NewChatMember`, `ViaJoinRequest`;
  `models.ChatMemberRestricted.IsMember`; `models.User.IsBot`.
- **П6.** Приоритеты очереди: `queue.PrioHigh=0` (удаление/бан), `PrioNormal=1`, `PrioLow=2`.
  `priorityFor` (`main.go:88`) даёт High только `DeleteMessages`, `BanMember`, `UnbanMember`,
  `RestrictMember`, `UnrestrictMember`, `BanSenderChat`, остальному Normal. Лимит на чат
  `perChatRateRPS = 1`, `perChatBurst = 3` (`main.go:55-56`) — рейд из сотни вступлений без
  ограничения растянул бы очередь чата на минуты.
- **П7.** Конфиг: `config.Parse` → `apply*Defaults` → `Validate`; `config.UnknownKeys` декодирует в
  `Config` с `KnownFields(true)`, поэтому новый блок признаётся автоматически, как только есть поле
  структуры. Прецедент карты по чату — `llm.prompt_overrides: map[int64]string` + `LLM.PromptFor`.
  Прецедент отказа — `chats.enforce` вне `chats.allowlist` в режиме allowlist → ошибка `Validate`.
  `config.Store.Current()` отдаёт живой конфиг после перезагрузки (`internal/config/reload.go`).
- **П8.** `telegram.RegisteredChat(cfg, chatID)` — допуск чата (allowlist/auto). `store.GetChat` →
  `(ChatRow{Enabled, DryRun…}, found, err)`. `store.TrustCount(chat, user)` — число осмысленных
  сообщений. `detect.BlocklistSource{ Listed(userID) bool }`; в `main.go:683` он nil при
  выключенном блоклисте. Схема БД — `internal/store/migrate.go`, `CREATE TABLE IF NOT EXISTS` в
  константе `schema`.
- **П9.** Тестовый приём перехвата запросов к Bot API — `httptest`-сервер в
  `internal/telegram/livept_test.go` (`sendAdminProbe`, `sendMessageOK`).

Предположения (не проверены вживую, исполнителю проверять НЕ нужно — живого Telegram в вехе нет):

- **П5.** Как Telegram сейчас обрабатывает старый `receiver_user_id`: игнорирует (сообщение
  становится публичным), отвергает или ещё принимает — неизвестно. Предохранитель §3.1 закрывает
  первый исход.
- **П10.** Эфемерное сообщение новичку в группе видно только ему и доходит не всегда (формулировка
  Bot API 10.2). Поэтому приветствие — информирование, а не проверка, и на его доставку ничего не
  завязываем.

## 3. Что сделать

### 3.1 Библиотека и безопасный `SendEphemeral`

- `./scripts/dev.sh get github.com/go-telegram/bot@v1.27.0 && ./scripts/dev.sh mod tidy`. Других
  зависимостей не добавлять и не поднимать сверх того, что потянет сама библиотека.
- `LivePort.SendEphemeral` шлёт `EphemeralMessageParameters: &models.EphemeralMessageParameters{ReceiverUserID: userID}`.
- Предохранитель: ответ Telegram без `ephemeral_message_id` (0), но с `message_id` ≠ 0 означает, что
  сообщение ушло в чат публично. Тогда удалить его через `p.DeleteMessages(ctx, chat, []int{id})`
  (через диспетчер, не прямым вызовом библиотеки) и вернуть ошибку-значение
  `telegram.ErrEphemeralNotHonored` (экспортируемая переменная, проверяется `errors.Is`). Ошибку
  удаления — обернуть в возвращаемую. Вызовы в `admin` и `incident` не менять: они уже логируют или
  игнорируют ошибку.

### 3.2 Метод порта для приветствия

- В `telegram.Port` добавить `SendWelcome(ctx context.Context, chat, userID int64, text string) (int, error)`.
  Live — та же отправка и тот же предохранитель, что у `SendEphemeral` (общий помощник, не копия), но
  имя метода для `prio` — `"SendWelcome"`. В `priorityFor` `"SendWelcome"` → `queue.PrioLow`:
  приветствие не должно задерживать удаление спама и карточки в админ-чате.
- Fake (`internal/telegram/fake`): `WelcomeID`, `WelcomeErr`, `LastWelcome{Chat, UserID, Text}`,
  запись вызова `"SendWelcome"` в журнал, как у `SendEphemeral`.
- Текст — обычный, без `parse_mode`: он приходит из конфига, разметка в нём не нужна.

### 3.3 Конфиг `welcome`

Новый верхнеуровневый блок `welcome` в `config.Config`:

```yaml
welcome:
  enabled: false        # общий выключатель для всех допущенных чатов
  text: ""              # общий текст
  max_per_minute: 20    # потолок приветствий на один чат за скользящие 60 с
  chats: {}             # по чату: -1001234567890: {enabled: true, text: "..."}
```

- Типы: `Welcome{Enabled *bool; Text string; MaxPerMinute *int; Chats map[int64]WelcomeChat}`,
  `WelcomeChat{Enabled *bool; Text string}`. Умолчания: `Enabled=false`, `MaxPerMinute=20`
  (явные значения не перетирать — как у прочих `*bool`/`*int`).
- `func (w Welcome) For(chatID int64) (text string, ok bool)`: если в `Chats` есть запись с
  `Enabled != nil` — решает она, иначе общий `Enabled`. Текст — непустой (после `TrimSpace`) текст
  записи чата, иначе общий. `ok` только при включённости И непустом тексте.
- `Validate` отказывает, называя ключ: `max_per_minute < 1`; включённый чат (общий выключатель или
  запись с `enabled: true`) с пустым итоговым текстом; текст длиннее 4096 символов (рун) — лимит
  сообщения Telegram; в режиме `chats.mode: allowlist` ключ `welcome.chats`, которого нет в
  `chats.allowlist` (та же логика, что у `chats.enforce`).

### 3.4 Кто считается вступившим

В `internal/telegram` (библиотечные типы разрешены только там):
`type JoinEvent struct{ ChatID, UserID int64; ViaJoinRequest bool }` и
`func JoinFromChatMemberUpdated(u models.ChatMemberUpdated) (JoinEvent, bool)`. `true` только если
новый участник — пользователь не-бот, старый статус `left` / `kicked` или `restricted` с
`is_member=false`, новый — `member` или `restricted` с `is_member=true`. Повышение в админы, смена
имени, выход, бан, вступление бота — `false`.

### 3.5 Учёт «уже приветствовали»

В `internal/store`: таблица `welcome_sent(chat_id, user_id, created_at, PRIMARY KEY(chat_id, user_id))`
в `schema`; методы `WasWelcomed(chatID, userID int64) (bool, error)` и
`MarkWelcomed(chatID, userID int64) error` (`INSERT OR IGNORE`, повтор не ошибка). Текст сообщений и
имена не хранить.

### 3.6 `watch.Welcomer`

Файл `internal/watch/welcome.go`. Зависимости: `*config.Store` (читать `Current()` на каждое
событие — так опция включается/выключается перезагрузкой конфига без рестарта), узкий интерфейс
стора (`WasWelcomed`, `MarkWelcomed`, `TrustCount`, `GetChat`), `telegram.Port`,
`detect.BlocklistSource` (может быть nil), `Now func() time.Time` (для теста окна).
`Observe(ctx, telegram.JoinEvent) (Outcome, error)`, где `Outcome` — строка-результат для метрики.
Порядок решений:

1. чат не допущен `RegisteredChat` → `skip_not_admitted`;
2. `Welcome.For` не включён → `skip_disabled`;
3. строка чата есть и `Enabled=false` → `skip_chat_disabled`; нет строки — продолжаем;
4. блоклист не nil и `Listed(user)` → `skip_blocklisted`;
5. `WasWelcomed` или `TrustCount > 0` → `skip_known` (ранее приветствовали или уже писал по делу);
6. в чате за последние 60 с уже `max_per_minute` отправок → `skip_rate_capped`;
7. `SendWelcome`; ошибка → `error`, **не** помечать приветствованным; успех → `MarkWelcomed` → `sent`.

Любая ошибка чтения стора → `error` без отправки (fail closed). Режим dry-run чата на приветствие
не влияет: это не санкция, а включается оно только явной опцией — так и написать в доке.

### 3.7 Подключение в `main.go`

В ветке `update.ChatMember`: после вычисления события вызвать `telegram.JoinFromChatMemberUpdated`
на inline-потребителе, а приветствие выполнять **в той же задаче секвенсора**, что и
`memberWatcher.Observe`, строго после него. Инвалидацию кэша админов не трогать. Чтобы это было
тестируемо, вынеси тело ветки в функцию пакета `main` (имя на твоё усмотрение) и покрой её тестом.
Метрика: `reg.IncCounter("tg_antispam_welcome_total", 1, "result", <Outcome>)`. Ошибки — `log.Printf`
без текста приветствия.

### 3.8 Документация

- `config.example.yaml`: блок `welcome` с комментариями — что это эфемерное сообщение, доставка не
  гарантирована, не зависит от dry-run, простой текст, пример записи в `chats` (вымышленный id),
  что в тексте стоит указать правила и контакт для разбора ошибочного мута.
- `deploy/helm/tg-antispam/values.yaml` (публичный чарт): `welcome: {enabled: false}` в строке
  `config` с одной строкой комментария. Версию чарта НЕ поднимать.
- `CHANGELOG.md`, раздел Unreleased: Added — приветствие; Changed — библиотека v1.27.0 и Bot API
  10.3; Fixed/Security — предохранитель публичного «эфемерного» сообщения.
- `docs/architecture.md`: абзац про путь `chat_member` → приветствие.

### 3.9 Тесты (имена обязательны, по ним идут критерии)

- `internal/telegram`: `TestSendEphemeralUsesEphemeralParameters` (в теле запроса есть
  `ephemeral_message_parameters.receiver_user_id`, верхнеуровневого `receiver_user_id` нет);
  `TestSendEphemeralDeletesPublicFallback` (сервер отвечает `message_id` без
  `ephemeral_message_id` → уходит удаление этого id и возвращается `ErrEphemeralNotHonored`);
  `TestSendWelcomeUsesEphemeralParameters`; `TestJoinFromChatMemberUpdated` (таблица всех случаев §3.4).
- `internal/config`: `TestWelcomeDefaultsOff`, `TestWelcomePerChatResolution`,
  `TestWelcomeValidate` (каждая ветка отказа §3.3 — своим подслучаем, с проверкой имени ключа в
  тексте ошибки), `TestConfigExampleParsesWithWelcomeOff` (разбирает `config.example.yaml`,
  `UnknownKeys` без ошибки, приветствие выключено).
- `internal/store`: `TestWelcomedRoundTrip` (в том числе повторный `MarkWelcomed` и `Migrate` на
  уже существующей БД).
- `internal/watch`: `TestWelcomerSendsOncePerChatUser`, `TestWelcomerSkips` (таблица шагов 1–5 с
  проверкой `Outcome` и отсутствия `SendWelcome`), `TestWelcomerRateCap` (потолок 2: три вступления →
  две отправки и `skip_rate_capped`; через 61 с по `Now` — снова отправка), `TestWelcomerSendFailureNotMarked`,
  `TestWelcomerStoreErrorFailsClosed`, `TestWelcomerFollowsConfigReload` (`Store.Swap` включает и
  выключает без пересоздания).
- `cmd/tg-antispam`: `TestChatMemberUpdateRunsIdentityWatchAndWelcome` (вступление → запись имени и
  приветствие, именно в этом порядке; смена имени без вступления → только запись имени);
  `TestPriorityForWelcomeIsLow`.

## 4. Не трогать

- `internal/incident`, `internal/admin`, `internal/detect`, `internal/queue`, `internal/blocklist`,
  `internal/llm`, `internal/ops`, `internal/selfcheck`, `internal/train`, `internal/domain`:
  `delete_mute`, кнопки разбора в админ-чате, `/spam`/`/ham` и каскад не меняются.
- `detection.ephemeral_notice_*` — отдельная существующая опция, не сливать с приветствием.
- `.github/`, `Dockerfile`, `Makefile`, `scripts/`, `deploy/helm/tg-antispam/Chart.yaml`,
  `deploy/helm/tg-antispam/templates/`, корпус и боевые values в других репозиториях.
- Никакой капчи, мута/кика вступивших, обработки `chat_join_request`, изменения `allowed_updates`.
- Исключение: `docs/specs/welcome-m1.md` и `docs/spec-queue.md` уже закоммичены — не править.
- Живой Telegram, прод, токены — не трогать. Host tmux, сигналы чужим процессам — запрещены.

## 5. Разрешения

Сеть — только модульный прокси Go через `./scripts/dev.sh`. Docker — через `./scripts/dev.sh` и
`golang:1.26.6` для `gofmt`. Новые файлы — только в путях из AC-010.

## 6. Критерии приёмки

- **AC-001.** Полный сьют с гонками зелёный:
  `bash -c './scripts/dev.sh test -race -count=1 ./...'`
- **AC-002.** vet, сборка и gofmt чистые:
  `bash -c './scripts/dev.sh vet ./... && ./scripts/dev.sh build ./... && test -z "$(docker run --rm -u "$(id -u):$(id -g)" -v "$PWD":/src -w /src golang:1.26.6 gofmt -l cmd internal)"'`
- **AC-003.** Библиотека v1.27.0, модули аккуратны и сверены:
  `bash -c 'grep -Eq "^[[:space:]]*github.com/go-telegram/bot v1\.27\.0$" go.mod && ./scripts/dev.sh mod tidy -diff && ./scripts/dev.sh mod verify'`
- **AC-004.** Эфемерная отправка и классификация вступления:
  `bash -c 'out=$(./scripts/dev.sh test -count=1 -v -run "^(TestSendEphemeralUsesEphemeralParameters|TestSendEphemeralDeletesPublicFallback|TestSendWelcomeUsesEphemeralParameters|TestJoinFromChatMemberUpdated)$" ./internal/telegram/ 2>&1) && for t in TestSendEphemeralUsesEphemeralParameters TestSendEphemeralDeletesPublicFallback TestSendWelcomeUsesEphemeralParameters TestJoinFromChatMemberUpdated; do printf "%s\n" "$out" | grep -Eq -- "--- PASS: $t( |$)" || exit 1; done'`
- **AC-005.** Конфиг приветствия:
  `bash -c 'out=$(./scripts/dev.sh test -count=1 -v -run "^(TestWelcomeDefaultsOff|TestWelcomePerChatResolution|TestWelcomeValidate|TestConfigExampleParsesWithWelcomeOff)$" ./internal/config/ 2>&1) && for t in TestWelcomeDefaultsOff TestWelcomePerChatResolution TestWelcomeValidate TestConfigExampleParsesWithWelcomeOff; do printf "%s\n" "$out" | grep -Eq -- "--- PASS: $t( |$)" || exit 1; done'`
- **AC-006.** Учёт приветствованных в БД:
  `bash -c 'out=$(./scripts/dev.sh test -count=1 -v -run "^TestWelcomedRoundTrip$" ./internal/store/ 2>&1) && printf "%s\n" "$out" | grep -Eq -- "--- PASS: TestWelcomedRoundTrip( |$)"'`
- **AC-007.** Решения `Welcomer`:
  `bash -c 'out=$(./scripts/dev.sh test -count=1 -v -run "^(TestWelcomerSendsOncePerChatUser|TestWelcomerSkips|TestWelcomerRateCap|TestWelcomerSendFailureNotMarked|TestWelcomerStoreErrorFailsClosed|TestWelcomerFollowsConfigReload)$" ./internal/watch/ 2>&1) && for t in TestWelcomerSendsOncePerChatUser TestWelcomerSkips TestWelcomerRateCap TestWelcomerSendFailureNotMarked TestWelcomerStoreErrorFailsClosed TestWelcomerFollowsConfigReload; do printf "%s\n" "$out" | grep -Eq -- "--- PASS: $t( |$)" || exit 1; done'`
- **AC-008.** Подключение в `main` и приоритет:
  `bash -c 'out=$(./scripts/dev.sh test -count=1 -v -run "^(TestChatMemberUpdateRunsIdentityWatchAndWelcome|TestPriorityForWelcomeIsLow)$" ./cmd/tg-antispam/ 2>&1) && for t in TestChatMemberUpdateRunsIdentityWatchAndWelcome TestPriorityForWelcomeIsLow; do printf "%s\n" "$out" | grep -Eq -- "--- PASS: $t( |$)" || exit 1; done'`
- **AC-009.** Документация и чарт: пример и чарт выключены, CHANGELOG упоминает приветствие:
  `bash -c 'grep -Eq "^welcome:" config.example.yaml && grep -Eq "^  welcome:" deploy/helm/tg-antispam/values.yaml && grep -A2 -E "^  welcome:" deploy/helm/tg-antispam/values.yaml | grep -Eq "enabled: false" && sed -n "/^## \[Unreleased\]/,/^## \[0/p" CHANGELOG.md | grep -qi "welcome" && grep -qi "welcome" docs/architecture.md'`
- **AC-010.** Состав работы в границах вехи, дерево чистое:
  `bash -c 'f=$(mktemp) && git diff --name-only 067cb086ceccde87bc8208a39ff63a4eab26bdd5..HEAD > "$f" && test -s "$f" && ! grep -qvE "^(go\.(mod|sum)|CHANGELOG\.md|README\.md|config\.example\.yaml|deploy/helm/tg-antispam/values\.yaml|docs/architecture\.md|docs/specs/welcome-m1\.md|docs/spec-queue\.md|cmd/tg-antispam/[a-z0-9_]+\.go|internal/(telegram|telegram/fake|config|store|watch)/[a-z0-9_]+\.go|internal/config/testdata/[a-z0-9_]+\.yaml)$" "$f" && test -z "$(git status --porcelain -- . ":(exclude)report.json" ":(exclude)report-blocked.md" ":(exclude)tmp")"'`

## 7. Контракт отчёта

`report.json` в корне клона, схемы v2, untracked. Имена полей дословные:

```json
{"schema_version": 2,
 "policy_id": "cross-review-v1",
 "handoff_status": "ready",
 "executor": {"backend": "grok|spark", "model": "<точная модель>"},
 "spec_sha256": "<sha256 файла docs/specs/welcome-m1.md>",
 "base_sha": "067cb086ceccde87bc8208a39ff63a4eab26bdd5",
 "reviewed_sha": "<коммит, ушедший ревьюерам>",
 "final_sha": "<HEAD клона>",
 "review": {"resolutions": [], "initial_receipts": [], "verification_receipts": []},
 "criteria": [{"id": "AC-001", "status": "pass|fail|blocked",
               "command": "<команда-доказательство>", "rc": 0, "note": "…"}]}
```

- поле называется `reviewed_sha`, не `review_sha`; все три sha — 40 строчных hex, `final_sha` равен
  `HEAD` клона, цепочка `base_sha → reviewed_sha → final_sha` идёт от предка к потомку;
- `handoff_status` — `ready`, `blocked` или `needs_owner`;
- в `criteria` ровно десять записей AC-001…AC-010, `command` совпадает с командой критерия
  посимвольно, каждая перезапущена на `final_sha`;
- у `blocked` в `rc` стоит `null`, в `note` — дословная ошибка;
- в `review.initial_receipts` / `review.verification_receipts` — только имена файлов квитанций.

## 8. Контракт на невыполнимое

Спека противоречит себе, критерий невыполним или факт опровергает §2 — **остановись и доложи**:
`report-blocked.md` в корне клона с дословной командой, выводом и опровергнутым пунктом. Обходить
несовместимость запрещено: `--no-deps`, `GOTOOLCHAIN=local`, `|| true`, `set +e` в команде
критерия, `sudo`, `git push`, ослабление или удаление чужих тестов, `replace` в `go.mod`.

Отдельно:

- v1.27.0 не собирается с кодом так, что правка требует трогать пути из §4 (кроме
  предусмотренного §3.1) — стоп, доложи список ошибок компиляции.
- Существующий тест падает из-за смены поведения библиотеки (v1.24–v1.27), а не из-за твоего кода —
  стоп, не правь чужой тест, доложи имя теста и вывод.
- Для любого пункта нужен живой Telegram — стоп.

## 9. Автономное перекрёстное ревью — обязательная часть вехи

Отчёт объявляет `policy_id: cross-review-v1`, приёмщик это проверяет. Без двух успешных квитанций
будет `blocked: initial: incomplete`.

1. Реализуй веху, прогони критерии, закоммить. `REVIEW_SHA` — `HEAD` после последнего рабочего
   коммита. Незакоммиченный код ревьюерам не передаётся.
2. Два ревью параллельно. Пара по твоему backend: Grok → `codex` + `agy`; Spark/Codex → `agy` + `grok`.

   ```
   bash /home/deploy/.claude/skills/executor-milestone/scripts/review_run.sh initial <ревьюер> \
     --clone /home/deploy/exec-clones/antispam-welcome-m1-20260924 \
     --base 067cb086ceccde87bc8208a39ff63a4eab26bdd5 \
     --range 067cb086ceccde87bc8208a39ff63a4eab26bdd5..<REVIEW_SHA> \
     --context "tg-antispam M1: эфемерное приветствие новичку опцией на чат + go-telegram/bot v1.27.0 и предохранитель публичного эфемерного; только чтение; tmux и процессы — только Docker с фейками"
   ```

3. `rc=0` ревью не доказывает. Отказ — пустой или оборванный ответ, ошибка квоты, нет строки
   вердикта. Вердикт — последнее вхождение `ВЕРДИКТ:` с `ПРИНЯТО` или `НЕ ПРИНИМАТЬ`. При временном
   сбое — один технический повтор; пустой stdout с `rc=124` — один повтор с поднятым таймаутом.
   Исчерпанная квота — `blocked`, без цикла повторов.
4. Каждую находку перепроверь по коду. Подтверждённые исправь, затронутые критерии прогони заново,
   закоммить. Заход исправлений ОДИН. Были правки — те же двое смотрят `<REVIEW_SHA>..<FINAL_SHA>`
   фазой `verify` той же командой. Не было — `verify` не нужна.
5. В `report.json`: `review.resolutions` — по записи на находку (`finding_id`, `decision` из
   `fixed|disproved|needs_owner`, `source_receipt`, `proof`, `fix_commits`); `fix_commits`
   обязателен при `fixed` и лежит в `reviewed_sha..final_sha`; у `disproved` в `proof` — команда и
   её вывод.

🚨 **После старта фазы `verify` не коммить ничего.**

## 10. Стыки с соседними вехами

- Координатор после приёмки: мутации по новым тестам противоположным исполнителем, живой стенд с
  тестовым ботом в тестовой группе (вступление → видно ли приветствие только вступившему, что
  возвращает Telegram на старый `receiver_user_id`), релиз 0.18.0 (Chart.appVersion + тег), затем
  включение `welcome` в боевых values `romtk3s/tg-antispam` по чатам и тексту владельца.
- M2 (капча) опирается на то, что даёт эта веха: `JoinFromChatMemberUpdated`, блок-образец
  конфига «общий выключатель + карта по чату», `SendWelcome`/предохранитель эфемерного, таблицу
  «уже проверенных» (M2 может расширить её, но не менять смысл «приветствовали»).
