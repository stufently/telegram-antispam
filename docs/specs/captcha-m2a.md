# Веха M2a: капча-кнопка на входе + приветствие вне секвенсора (опции на чат)

- Репозиторий `/home/deploy/github/telegram-antispam` (публичный `stufently/telegram-antispam`), дата 26.09.2026.
- BASE_SHA: `625b727a6bf3f6d4279f59b5a27fa785700f4d07` («test: close welcome M1 mutation coverage gaps»).
  Клон снят ровно с BASE. Эта спека в клоне НЕ лежит и в дифф не входит: читай её по абсолютному
  пути `/home/deploy/github/telegram-antispam/docs/specs/captcha-m2a.md`. BASE — левая граница
  ревью-диффа и значение `base_sha` в отчёте.
- Исполнитель: `gk` (Grok). Исправления по ревью — тот же исполнитель. Мутации по новым тестам гоняет
  противоположный исполнитель (это делает координатор, не ты).
- Критериев: 11.
- Решение владельца 24.09.2026 (`docs/spec-queue.md`): капча — опцией на чат, по умолчанию выключена;
  приветствие — вне задачи секвенсора, асинхронно. Эта веха — режим `button` и вынос приветствия.
  Режим `join_request` — следующая веха M2b; здесь он отвергается конфигом.

## 0. Где работать

Одноразовый клон `/home/deploy/exec-clones/antispam-captcha-m2a-20260926`, новая ветка `captcha-m2a`.
Живое дерево `/home/deploy/github/telegram-antispam` не трогать. **Push в origin запрещён: работу
заберёт постановщик через `git fetch` из клона.** Коммиты локальные, `git add` только по именам
файлов, никогда `-A`. Прочитай `AGENTS.md` и `docs/architecture.md` до первой правки.

Кэш модулей `.gopath/` постановщик положил до запуска (`./scripts/dev.sh mod download`), он в
`.gitignore`. Новых зависимостей в этой вехе нет, `go.mod`/`go.sum` не меняются. Каталог `tmp/` в
клоне — для улик ревью, не коммитить.

## 1. Задача и почему

1. **Приветствие блокирует модерацию чата.** Сейчас `handleChatMember`
   (`cmd/tg-antispam/chatmember.go`) внутри per-chat задачи секвенсора зовёт `Welcomer.Observe`, а
   тот синхронно ждёт `SendWelcome` (приоритет `PrioLow`, лимит чата 1 rps). При рейде вступлений
   задачи удаления спама того же чата стоят за приветствиями, а при полной очереди секвенсора
   (1024 на чат, `internal/telegram/sequencer.go`) задачи сбрасываются. Решения и учёт остаются в
   задаче чата, **отправка уходит в отдельную горутину**.
2. **Капча-кнопка.** Вступивший в чат с включённой капчей глушится (`RestrictMember`), получает
   кнопку «я не бот»; нажал сам — мут снят; не нажал до дедлайна — `on_fail` (`kick` или
   `keep_muted`). Дедлайны хранятся в SQLite и переживают рестарт. Капча — отдельный путь, не
   инцидент: админ-чат, `delete_mute`, кнопки разбора и `/spam` не трогаются.

## 2. Что проверено постановщиком 26.09.2026, а что предположение

Проверено чтением кода на BASE и модуля `go-telegram/bot@v1.27.0` из `.gopath`:

- **П1.** `handleChatMember(ctx, cm, invalidate, submit, members, welcomer, count)` — инвалидация кэша
  админов на inline-потребителе, затем одна задача `submit(chatID, …)`: `members.Observe`, потом
  `welcomer.Observe` и `count(outcome)` (метрика `tg_antispam_welcome_total{result}`). Тест —
  `cmd/tg-antispam/chatmember_test.go`.
- **П2.** `watch.Welcomer.Observe` (`internal/watch/welcome.go`): шаги 1–6 — чтение конфига/стора,
  затем `SendWelcome`, `recordSend` (время ЗАВЕРШЕНИЯ попытки, в т.ч. неудачной), `MarkWelcomed`.
  Потолок — `max_per_minute` за скользящие 60 с по `Now`.
- **П3.** Callback'и: `main.go` ветка `update.CallbackQuery` всё отдаёт `adminHandler.Handle` через
  `seq.Submit(AdminChatID, …)`. `admin.ParseCallback` принимает только `act:key` с известными `act`
  (`fp`-подобные коды из `admin.Action`), неизвестное → ответ «invalid callback». Data кнопки ≤ 64 байт.
- **П4.** Порт: `RestrictMember(ctx, chat, user, Perms{CanSend:false}, until)` (`until=0` — бессрочно),
  `UnrestrictMember` (снимает мут, выдав все права), `BanMember`, `UnbanMember` (`OnlyIfBanned: true` —
  после бана это «кик»: человек может вернуться), `DeleteMessages`, `AnswerCallback`. Эфемерная
  отправка — `LivePort.sendToUser` с предохранителем `ErrEphemeralNotHonored` (публичная копия
  удаляется). `toInlineKeyboard(buttons)` есть. Все вызовы идут через диспетчер (`submitSync*`,
  `p.prio(method)`); прямой вызов диспетчера из задачи диспетчера — дедлок (см. коммент `sendToUser`).
- **П5.** v1.27.0: `SendMessageParams.ReplyMarkup` и `.EphemeralMessageParameters`;
  `bot.DeleteEphemeralMessage(ctx, &bot.DeleteEphemeralMessageParams{ChatID, ReceiverUserID, EphemeralMessageID})`.
  `models.CallbackQuery{ID, From, Message, Data}`.
- **П6.** `JoinFromChatMemberUpdated` (`internal/telegram/join.go`): только не-бот, из
  left/kicked/restricted(is_member=false) в member/restricted(is_member=true). Администраторы и
  создатель вступлением не считаются — отдельная проверка «админ» в капче не нужна.
- **П7.** Конфиг: образец — блок `welcome` (`config.Welcome`, `For`, `applyWelcomeDefaults`,
  `validateWelcome`), тип `config.Duration` (yaml `2m`), проверка «ключ чата вне `chats.allowlist`».
  Эффективный dry-run: `cfg.Chats.DryRunFor(chatID, stored)`, где `stored` — `ChatRow.DryRun` из
  `store.GetChat`, а при отсутствии строки — `cfg.Chats.DryRunDefault()`.
- **П8.** Стор: `store.Open(path)`, `db.Migrate()`, запись через `db.Write(func(*sql.Tx) error)`,
  чтение `db.Read()`; схема — константа `schema` в `internal/store/migrate.go`
  (`CREATE TABLE IF NOT EXISTS`). Таблица `welcome_sent` — прецедент.
- **П9.** Завершение: `b.Start` → ожидание фоновых производителей → таймер дренажа
  `gracefulShutdownTimeout` → `handler.Stop()`, `seq.Wait()` → `stopWork()` → `<-dispDone`.
- **П10.** Блоклист на вступлении: `detect.BlocklistSource.Listed`; nil при выключенном блоклисте.
  Штатный путь блоклиста — каскад на первом сообщении (`internal/detect/cascade.go`), капча его не
  дублирует.

Предположения (живого Telegram в вехе нет, проверяет координатор на стенде):

- **П11.** Эфемерное сообщение с inline-клавиатурой доставляется и нажатие приходит обычным
  `callback_query` с `From` = получатель. Не доставилось молча — вступивший не увидит кнопку, поэтому
  при ошибке отправки есть запасной путь (обычное сообщение), а тихая недоставка закрывается
  таймаутом с `on_fail` (для `kick` вступивший может вернуться).

## 3. Что сделать

### 3.1 Приветствие вне секвенсора (`internal/watch/welcome.go`, `chatmember.go`)

- Разделить `Observe` на решение и доставку, `Observe` оставить (синхронная композиция, существующие
  тесты M1 не менять):
  - `func (w *Welcomer) Admit(ctx, ev telegram.JoinEvent) (Outcome, *WelcomeTicket, error)` — шаги 1–6
    как сейчас; при допуске возвращает `OutcomeQueued` (`"queued"`) и непустой билет, **резервируя**
    слот: потолок считает `len(окно) + в_полёте[chat] >= max`; пара (chat,user) в полёте даёт
    `skip_known`.
  - `func (w *Welcomer) Deliver(ctx, t *WelcomeTicket) (Outcome, error)` — `SendWelcome`, снять резерв,
    `recordSend(время завершения)`, при успехе `MarkWelcomed` → `sent`, иначе `error` (без пометки).
  - `func (w *Welcomer) DeliverAsync(ctx, t *WelcomeTicket, done func(Outcome, error))` — `Deliver` в
    отдельной горутине, учтённой во внутреннем `sync.WaitGroup`; `func (w *Welcomer) Wait()`.
- `handleChatMember`: в задаче чата — `members.Observe`, затем капча (§3.6), затем `welcomer.Admit`.
  Пропуск/ошибка решения → `count(outcome)` сразу; `queued` не считается — считается итог из `done`
  (`sent`/`error`). Задача чата не ждёт отправки.
- В `main.go` при завершении после `seq.Wait()` дождаться `welcomer.Wait()` и `captcha.Wait()` (в
  пределах того же таймера дренажа), затем `stopWork()`.

### 3.2 Конфиг `captcha`

```yaml
captcha:
  enabled: false            # общий выключатель для всех допущенных чатов
  mode: button              # в этой версии только button; join_request — отказ Validate
  timeout: 2m               # сколько ждать нажатия
  on_fail: kick             # kick | keep_muted
  text: "..."               # текст с кнопкой (умолчание — английская фраза, см. ниже)
  button_text: "..."        # надпись кнопки (умолчание "I am not a bot")
  chats: {}                 # по чату: -1001234567890: {enabled: true, timeout: 5m, on_fail: keep_muted, text: "...", button_text: "..."}
```

- Типы: `Captcha{Enabled *bool; Mode string; Timeout Duration; OnFail string; Text, ButtonText string;
  Chats map[int64]CaptchaChat}`, `CaptchaChat{Enabled *bool; Mode string; Timeout Duration; OnFail,
  Text, ButtonText string}`. Умолчания (`applyCaptchaDefaults`, явные значения не перетирать):
  `Enabled=false`, `Mode="button"`, `Timeout=2m`, `OnFail="kick"`,
  `Text="Press the button below to confirm you are not a bot, otherwise you will be removed from the chat."`,
  `ButtonText="I am not a bot"`.
- `type CaptchaPolicy struct{ Mode string; Timeout time.Duration; OnFail, Text, ButtonText string }` и
  `func (c Captcha) For(chatID int64) (CaptchaPolicy, bool)`: `Enabled` записи чата (не nil) решает,
  иначе общий; каждое непустое/ненулевое поле записи чата перекрывает общее; `ok` только при
  включённости.
- `Validate` (отказ называет ключ): `mode` (общий и в записи) не `button` — ошибка с текстом
  `not supported`, в т.ч. для `join_request`; `on_fail` не из `kick|keep_muted`; `timeout` вне
  `[30s, 1h]` (для записи — если задан); `text` пустой после `TrimSpace` при итоговой включённости
  или длиннее 4096 рун; `button_text` пустой при включённости или длиннее 64 рун; ключ
  `captcha.chats`, которого нет в `chats.allowlist` в режиме allowlist.

### 3.3 Порт: три метода (`internal/telegram`)

- `SendCaptchaEphemeral(ctx, chat, userID int64, text string, buttons [][]Button) (int, error)` —
  эфемерное сообщение с `ReplyMarkup` через **тот же** помощник, что `sendToUser` (расширить его
  необязательной клавиатурой, не копировать), с тем же предохранителем `ErrEphemeralNotHonored`.
- `SendCaptchaMessage(ctx, chat int64, text string, buttons [][]Button) (int, error)` — обычное
  сообщение в чат с клавиатурой, возвращает `message_id`.
- `DeleteEphemeral(ctx, chat, userID int64, ephemeralID int) error` — `deleteEphemeralMessage`.
- Все три — через диспетчер, приоритет по умолчанию (`PrioNormal`), `priorityFor` не менять.
- Fake: `CaptchaEphemeralID`, `CaptchaEphemeralErr`, `CaptchaMessageID`, `CaptchaMessageErr`,
  `DeleteEphemeralErr`, `LastCaptchaEphemeral{Chat, UserID, Text, Buttons}`,
  `LastCaptchaMessage{Chat, Text, Buttons}`, `LastDeleteEphemeral{Chat, UserID, EphemeralID}`,
  запись вызовов в журнал, как у прочих методов.
- `JoinEvent` получает поле `Restricted bool` — новый статус `restricted` (вступил уже с
  ограничениями). `JoinFromChatMemberUpdated` его заполняет, остальное поведение прежнее.
- `type MemberChange struct{ ChatID, UserID, ActorID int64 }` и
  `func MemberChangeFromUpdate(u models.ChatMemberUpdated) (MemberChange, bool)`: `UserID` — участник
  из `NewChatMember`, `ActorID` — `u.From.ID` (кто изменил); `false`, если участника нет.

### 3.4 Стор: таблица `captcha_challenges`

`captcha_challenges(chat_id, user_id, attempt INTEGER NOT NULL, state TEXT NOT NULL, deadline INTEGER
NOT NULL, fail_action TEXT NOT NULL DEFAULT '', tries INTEGER NOT NULL DEFAULT 0, ephemeral_id INTEGER
NOT NULL DEFAULT 0, message_id INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL, updated_at,
PRIMARY KEY(chat_id, user_id))` в `schema`. Имена и тексты не хранить. Состояния: `new` (вставлена,
мут ещё не подтверждён), `challenged` (заглушён, ждём нажатия), `passed`, `failing` (санкция `on_fail`
или снятие мута назначены, но ещё не выполнены — повторяемо), `failed`, `cancelled`.
Тип `store.CaptchaRow{ChatID, UserID int64; Attempt int64; State string; Deadline int64; FailAction
string; Tries int; EphemeralID, MessageID int; CreatedAt int64}`. Каждое изменение — один условный
`INSERT`/`UPDATE` в `db.Write`; `attempt` — счётчик попыток пары, растёт на каждом новом старте.

- `BeginCaptcha(chat, user, now, deadline int64) (CaptchaRow, bool, error)` — строки нет → вставить
  `new`, `attempt=1`; строка `failed`/`cancelled` → перезаписать в `new`, `attempt+1`, новые
  `created_at`/`deadline`, `tries=0`, `fail_action=''`, id = 0; `new`/`challenged`/`failing`/`passed` →
  `false` без изменений.
- `CaptchaPassed(chat, user) (bool, error)`; `GetCaptcha(chat, user) (CaptchaRow, bool, error)`.
- `TransitionCaptcha(chat, user, attempt int64, from []string, to string) (CaptchaRow, bool, error)` —
  перевод только при совпадении `attempt` и состояния из `from`; `bool` — перевёл ли этот вызов.
- `FailCaptcha(chat, user, attempt int64, from []string, action string) (CaptchaRow, bool, error)` —
  то же в `failing` с записью `fail_action` (`unrestrict` | `kick` | `keep_muted`).
- `RetryCaptcha(chat, user, attempt, nextDeadline int64) error` — для `failing`: `tries+1`, новый дедлайн.
- `SetCaptchaPrompt(chat, user, attempt int64, ephemeralID, messageID int) (CaptchaRow, error)` —
  записать id (состояние не менять), вернуть строку.
- `DueCaptchas(now int64, limit int) ([]CaptchaRow, error)` — `new`/`challenged`/`failing` с
  `deadline <= now`, по дедлайну.
- `SanctionSince(chat, user, since int64) (bool, error)` — есть ли строка `incidents` этой пары с
  `dry_run=0` и `created_at >= since` (модерация уже наказала вступившего — капча не вправе снять мут).

### 3.5 `watch.Captcha` (`internal/watch/captcha.go`)

Поля: `Config *config.Store` (читать `Current()` на каждое событие), `Store` — узкий интерфейс (методы
§3.4 + `TrustCount`, `GetChat`; `*store.DB` его реализует), `Port telegram.Port`, `Blocklist
detect.BlocklistSource` (может быть nil), `SelfID int64` (id бота), `Now func() time.Time`, `Count
func(result string)` (метрика для исходов, которые доходят в горутинах; nil допустим). Исход —
`CaptchaOutcome` (строка, метка `tg_antispam_captcha_total{result}`). Внутренний `sync.WaitGroup`,
`func (c *Captcha) Go(fn func())` (учтённая горутина), `Wait()`, и множество «в полёте» — пары, чей
`RestrictMember` ещё выполняется в этом процессе.

Callback data: `cap:<chatID>:<userID>:<attempt>`; `func IsCaptchaCallback(data string) bool` — префикс `cap:`.

**Исполнитель неудачи** `fail(row)` (для строки `failing`): `unrestrict` → `UnrestrictMember`, успех →
`cancelled`, исход `released`; `kick` → `BanMember`, затем `UnbanMember`, успех → `failed`,
`failed_kick`; `keep_muted` → сразу `failed`, `failed_muted`. Ошибка вызова → `RetryCaptcha(now+60s)`,
исход `error`; при `tries >= 5` → конечное состояние действия без вызова, исход `gave_up`, лог.

**Удаление запроса:** `EphemeralID != 0` → `DeleteEphemeral`; `MessageID != 0` → `DeleteMessages`.
Ошибки удаления — лог, не меняют исход.

**`OnJoin(ctx, ev telegram.JoinEvent, displayName string) (CaptchaOutcome, error)`** — решение в задаче
чата, сеть — в горутине через `Go`:

1. чат не допущен `RegisteredChat` → `skip_not_admitted`;
2. `Captcha.For` не включён → `skip_disabled`;
3. строка чата есть и `Enabled=false` → `skip_chat_disabled`;
4. эффективный dry-run чата (П7) → `skip_dry_run` (капча — санкция: мут/кик);
5. `ev.Restricted` → `skip_restricted` (уже ограничен — чужой мут не снимать);
6. блоклист не nil и `Listed` → `skip_blocklisted` (штатный путь блоклиста, П10);
7. `CaptchaPassed` или `TrustCount > 0` → `skip_known`. `WasWelcomed` **не** основание: приветствие
   помечается и у того, кто капчу провалил, — иначе кик и повторный вход обходили бы проверку;
8. `BeginCaptcha(now, now+timeout)` вернул `false` → `skip_pending`;
9. иначе `challenged`: пара — в «в полёте», и в горутине `Go`:
   a. `RestrictMember(CanSend:false, until 0)`; ошибка → `TransitionCaptcha(new→cancelled)`, исход
      `error`, конец;
   b. `TransitionCaptcha(new→challenged)`; снять пару из «в полёте»; не перевёл — конец;
   c. `SendCaptchaEphemeral` (текст политики, одна кнопка `ButtonText`, data с `attempt`); ошибка
      (любая, включая `ErrEphemeralNotHonored`) → `SendCaptchaMessage` с текстом
      `displayName + ", " + text` (пустое имя — просто текст); обе неудачны → **fail open**:
      `FailCaptcha(challenged→failing, unrestrict)` и `fail(row)`;
   d. отправилось → `SetCaptchaPrompt`; строка уже не `challenged` (нажали, отменили, истекла) →
      удалить только что отправленный запрос.

Любая ошибка чтения стора в шагах 3–8 → `error` без мута (fail open).

**`OnMemberChange(ctx, ch telegram.MemberChange) (CaptchaOutcome, error)`** — на КАЖДЫЙ `chat_member`
чата, до `OnJoin`: строка пары в `new`/`challenged` и `ActorID` не равен ни `SelfID`, ни `UserID`
(участника изменил админ: снял/наложил ограничение, забанил, повысил) → `TransitionCaptcha(→cancelled)`,
удалить запрос, исход `cancelled_by_admin`; мут бот при этом не трогает. Иначе `skip` (без метрики).

**`OnPress(ctx, cb CaptchaPress) (CaptchaOutcome, error)`**, `CaptchaPress{ID, Data string; PresserID int64}`.
Каждый путь отвечает на callback ровно один раз:

1. битая data → `invalid`; `PresserID != userID` → «not for you», `wrong_user`, состояние не менять;
2. строки нет, `attempt` не совпадает или состояние не `challenged` → `expired`;
3. `SanctionSince(chat, user, row.CreatedAt)` → `TransitionCaptcha(challenged→cancelled)`, удалить
   запрос, ответ «moderators will review», `cancelled_sanctioned` (мут остаётся — его снимает разбор);
4. `TransitionCaptcha(attempt, challenged→passed)` не перевёл → `expired`; перевёл →
   `UnrestrictMember`; ошибка → `TransitionCaptcha(passed→challenged)`, «try again», `error`;
   успех → удалить запрос, ответ, `passed`.

**`Sweep(ctx) (int, error)`** — `DueCaptchas(now, 100)`, по строке:

- `new`, пара «в полёте» в этом процессе → пропустить (мут ещё выполняется); `new` не в полёте
  (сирота после рестарта) → `FailCaptcha(new→failing, unrestrict)` и `fail`;
- `challenged` → если сейчас чат не допущен, капча для него выключена, строка чата `Enabled=false` или
  эффективный dry-run → `FailCaptcha(→failing, unrestrict)` (fail open); иначе если `SanctionSince` →
  `TransitionCaptcha(→cancelled)`, `cancelled_sanctioned`; иначе `FailCaptcha(→failing, on_fail
  текущей политики чата)`; перевёл → удалить запрос и `fail`;
- `failing` → `fail` (повтор после ошибки).

Возвращает число обработанных строк. Нажатие, отмена и таймаут одной строки: действует только тот,
чей условный переход перевёл.

### 3.6 Подключение в `main.go`

- Создать `captcha := &watch.Captcha{…, SelfID: selfID}` рядом с `welcomer`, передать в
  `handleChatMember` (новый параметр). В задаче чата порядок: `members.Observe` →
  `captcha.OnMemberChange` (для любого `chat_member` с участником) → `captcha.OnJoin` (для вступления,
  `displayName` — из `MemberFromChatMember`) → `welcomer.Admit`. Метрика
  `tg_antispam_captcha_total{result}` для исходов `OnJoin`/`OnMemberChange` (кроме `skip`)/`OnPress`/
  `Sweep`/`Count`.
- Ветка `update.CallbackQuery`: `watch.IsCaptchaCallback(cb.Data)` → `captcha.Go(func(){ captcha.OnPress(workCtx, …) })`
  **без** секвенсора и без админ-обработчика; иначе — как было. Вынеси маршрутизацию в функцию пакета
  `main` для теста.
- Фоновый производитель на `signalCtx`: тикер 5 с → `captcha.Sweep(workCtx)`; первый проход сразу
  после старта (дедлайны, истёкшие за время простоя).
- `allowed_updates` не менять.

### 3.7 Документация

- `config.example.yaml`: блок `captcha` с комментариями (выключено; мут до нажатия; эфемерная кнопка с
  запасным обычным сообщением; уважает dry-run; `on_fail`; пример записи чата с вымышленным id;
  `join_request` появится позже).
- `deploy/helm/tg-antispam/values.yaml`: `captcha: {enabled: false}` в строке `config` с одной строкой
  комментария. Версию чарта НЕ поднимать.
- `CHANGELOG.md`, Unreleased: Added — капча-кнопка; Changed — приветствие отправляется вне задачи чата.
- `docs/architecture.md`: абзац про капчу (состояния, дедлайны в SQLite, sweep, маршрут callback) и
  про асинхронное приветствие.

### 3.8 Тесты (имена обязательны, по ним идут критерии)

- `internal/config`: `TestCaptchaDefaultsOff`, `TestCaptchaPerChatResolution`, `TestCaptchaValidate`
  (каждая ветка отказа §3.2 — свой подслучай с проверкой имени ключа в ошибке),
  `TestConfigExampleParsesWithCaptchaOff`.
- `internal/store`: `TestCaptchaLifecycle` (вставка `attempt=1`; повтор при `new`/`challenged`/
  `failing`/`passed` → false; `failed` и `cancelled` → снова `new` c `attempt+1`; переход только из
  разрешённых и при своём `attempt`; второй одинаковый переход → false; `FailCaptcha`/`RetryCaptcha`;
  `DueCaptchas` по дедлайну и состояниям; `SanctionSince` по `dry_run` и времени; `Migrate` дважды).
- `internal/telegram`: `TestSendCaptchaEphemeralHasKeyboard` (в теле `ephemeral_message_parameters` и
  `reply_markup` с data кнопки), `TestSendCaptchaMessageHasKeyboard`, `TestDeleteEphemeralParams`,
  `TestJoinEventRestrictedFlag`, `TestMemberChangeFromUpdate`.
- `internal/watch` (реальный SQLite во временном каталоге + `fake.Port`): `TestCaptchaSkips` (таблица
  шагов 1–8 с проверкой исхода и отсутствия `RestrictMember`; отдельный случай: `WasWelcomed` капчу
  НЕ пропускает), `TestCaptchaMutesThenPrompts` (порядок `RestrictMember` → `SendCaptchaEphemeral`,
  строка `challenged`, data с `attempt`), `TestCaptchaFallsBackToChatMessage`,
  `TestCaptchaPromptFailureFailsOpen`, `TestCaptchaPressPasses`, `TestCaptchaPressByOtherUserRejected`,
  `TestCaptchaStaleAttemptIgnored` (кнопка прошлой попытки после повторного входа → `expired`, мут не
  снят), `TestCaptchaPressAfterSanctionKeepsMute`, `TestCaptchaAdminChangeCancels` (действие админа →
  `cancelled_by_admin`, ни снятия мута, ни кика; действие самого бота и самого участника — не отменяет),
  `TestCaptchaTimeoutKicks` (`BanMember` затем `UnbanMember`, строка `failed`),
  `TestCaptchaKickRetriedAfterError` (ошибка `UnbanMember` → строка `failing`, следующий `Sweep` после
  дедлайна повторяет и доводит до `failed`), `TestCaptchaTimeoutKeepMuted`,
  `TestCaptchaSweepFailsOpenWhenDryRunNow` (dry-run включили во время ожидания → снятие мута, без кика),
  `TestCaptchaSweepSkipsInFlightRestrict` (`RestrictMember` блокирован — `Sweep` строку `new` не трогает),
  `TestCaptchaPressAfterTimeoutIgnored`, `TestCaptchaSurvivesRestart` (новый `Captcha` на той же БД
  доводит истёкшую `challenged` и сироту `new`), `TestWelcomerAdmitReservesInFlight` (потолок 1: билет
  в полёте → второй `skip_rate_capped`; тот же user в полёте → `skip_known`).
- `cmd/tg-antispam`: `TestChatMemberWelcomeDoesNotBlockSequencer` (fake `SendWelcome` блокируется до
  сигнала — задача чата возвращается раньше, после сигнала метрика `sent`),
  `TestChatMemberRunsCaptchaBeforeWelcome`, `TestCallbackRoutingCaptchaVsAdmin`.

## 4. Не трогать

- `internal/incident`, `internal/admin`, `internal/detect`, `internal/queue`, `internal/blocklist`,
  `internal/llm`, `internal/ops`, `internal/selfcheck`, `internal/train`, `internal/domain`.
- Существующие тесты M1 (`welcome_test.go` в watch/config/store, `chatmember_test.go`) не ослаблять и не
  удалять; поправить вызов `handleChatMember` под новую сигнатуру можно.
- `.github/`, `Dockerfile`, `Makefile`, `scripts/`, `go.mod`, `go.sum`, `deploy/helm/tg-antispam/Chart.yaml`,
  `deploy/helm/tg-antispam/templates/`, `docs/specs/`, `docs/spec-queue.md`.
- `chat_join_request`, `allowed_updates`, Join Request Queries — вехa M2b.
- Живой Telegram, прод, токены — не трогать. Host tmux, сигналы чужим процессам — запрещены.

## 5. Разрешения

Сеть — только модульный прокси Go через `./scripts/dev.sh` (новых модулей нет). Docker — через
`./scripts/dev.sh` и `golang:1.26.6` для `gofmt`. Новые файлы — только в путях из AC-010.
Собственный дифф `BASE..HEAD` (он же уходит ревьюерам) — не больше 95000 байт (AC-011): ревью режет
вход на 100000 байт. Тесты — табличные и без дублирования обвязки.

## 6. Критерии приёмки

- **AC-001.** Полный сьют с гонками зелёный:
  `bash -c './scripts/dev.sh test -race -count=1 ./...'`
- **AC-002.** vet, сборка и gofmt чистые:
  `bash -c './scripts/dev.sh vet ./... && ./scripts/dev.sh build ./... && test -z "$(docker run --rm -u "$(id -u):$(id -g)" -v "$PWD":/src -w /src golang:1.26.6 gofmt -l cmd internal)"'`
- **AC-003.** Конфиг капчи:
  `bash -c 'out=$(./scripts/dev.sh test -count=1 -v -run "^(TestCaptchaDefaultsOff|TestCaptchaPerChatResolution|TestCaptchaValidate|TestConfigExampleParsesWithCaptchaOff)$" ./internal/config/ 2>&1) && for t in TestCaptchaDefaultsOff TestCaptchaPerChatResolution TestCaptchaValidate TestConfigExampleParsesWithCaptchaOff; do printf "%s\n" "$out" | grep -Eq -- "--- PASS: $t( |$)" || exit 1; done'`
- **AC-004.** Состояния капчи в БД:
  `bash -c 'out=$(./scripts/dev.sh test -count=1 -v -run "^TestCaptchaLifecycle$" ./internal/store/ 2>&1) && printf "%s\n" "$out" | grep -Eq -- "--- PASS: TestCaptchaLifecycle( |$)"'`
- **AC-005.** Методы порта и флаг вступления:
  `bash -c 'out=$(./scripts/dev.sh test -count=1 -v -run "^(TestSendCaptchaEphemeralHasKeyboard|TestSendCaptchaMessageHasKeyboard|TestDeleteEphemeralParams|TestJoinEventRestrictedFlag|TestMemberChangeFromUpdate)$" ./internal/telegram/ 2>&1) && for t in TestSendCaptchaEphemeralHasKeyboard TestSendCaptchaMessageHasKeyboard TestDeleteEphemeralParams TestJoinEventRestrictedFlag TestMemberChangeFromUpdate; do printf "%s\n" "$out" | grep -Eq -- "--- PASS: $t( |$)" || exit 1; done'`
- **AC-006.** Решения, отмены и таймауты капчи:
  `bash -c 'out=$(./scripts/dev.sh test -count=1 -v -run "^(TestCaptchaSkips|TestCaptchaMutesThenPrompts|TestCaptchaFallsBackToChatMessage|TestCaptchaPromptFailureFailsOpen|TestCaptchaPressPasses|TestCaptchaPressByOtherUserRejected|TestCaptchaStaleAttemptIgnored|TestCaptchaPressAfterSanctionKeepsMute|TestCaptchaAdminChangeCancels|TestCaptchaTimeoutKicks|TestCaptchaKickRetriedAfterError|TestCaptchaTimeoutKeepMuted|TestCaptchaSweepFailsOpenWhenDryRunNow|TestCaptchaSweepSkipsInFlightRestrict|TestCaptchaPressAfterTimeoutIgnored|TestCaptchaSurvivesRestart)$" ./internal/watch/ 2>&1) && for t in TestCaptchaSkips TestCaptchaMutesThenPrompts TestCaptchaFallsBackToChatMessage TestCaptchaPromptFailureFailsOpen TestCaptchaPressPasses TestCaptchaPressByOtherUserRejected TestCaptchaStaleAttemptIgnored TestCaptchaPressAfterSanctionKeepsMute TestCaptchaAdminChangeCancels TestCaptchaTimeoutKicks TestCaptchaKickRetriedAfterError TestCaptchaTimeoutKeepMuted TestCaptchaSweepFailsOpenWhenDryRunNow TestCaptchaSweepSkipsInFlightRestrict TestCaptchaPressAfterTimeoutIgnored TestCaptchaSurvivesRestart; do printf "%s\n" "$out" | grep -Eq -- "--- PASS: $t( |$)" || exit 1; done'`
- **AC-007.** Резерв приветствия и прежние тесты M1:
  `bash -c 'out=$(./scripts/dev.sh test -count=1 -v -run "^(TestWelcomerAdmitReservesInFlight|TestWelcomerSendsOncePerChatUser|TestWelcomerRateCap|TestWelcomerSendFailureNotMarked)$" ./internal/watch/ 2>&1) && for t in TestWelcomerAdmitReservesInFlight TestWelcomerSendsOncePerChatUser TestWelcomerRateCap TestWelcomerSendFailureNotMarked; do printf "%s\n" "$out" | grep -Eq -- "--- PASS: $t( |$)" || exit 1; done'`
- **AC-008.** Подключение в `main`:
  `bash -c 'out=$(./scripts/dev.sh test -count=1 -v -run "^(TestChatMemberWelcomeDoesNotBlockSequencer|TestChatMemberRunsCaptchaBeforeWelcome|TestCallbackRoutingCaptchaVsAdmin|TestChatMemberUpdateRunsIdentityWatchAndWelcome)$" ./cmd/tg-antispam/ 2>&1) && for t in TestChatMemberWelcomeDoesNotBlockSequencer TestChatMemberRunsCaptchaBeforeWelcome TestCallbackRoutingCaptchaVsAdmin TestChatMemberUpdateRunsIdentityWatchAndWelcome; do printf "%s\n" "$out" | grep -Eq -- "--- PASS: $t( |$)" || exit 1; done'`
- **AC-009.** Документация и чарт: пример и чарт выключены, CHANGELOG и архитектура упоминают капчу:
  `bash -c 'grep -Eq "^captcha:" config.example.yaml && grep -Eq "^  captcha:" deploy/helm/tg-antispam/values.yaml && grep -A2 -E "^  captcha:" deploy/helm/tg-antispam/values.yaml | grep -Eq "enabled: false" && sed -n "/^## \[Unreleased\]/,/^## \[0/p" CHANGELOG.md | grep -qi "captcha" && grep -qi "captcha" docs/architecture.md'`
- **AC-010.** Состав работы в границах вехи, дерево чистое:
  `bash -c 'f=$(mktemp) && git diff --name-only 625b727a6bf3f6d4279f59b5a27fa785700f4d07..HEAD > "$f" && test -s "$f" && ! grep -qvE "^(CHANGELOG\.md|README\.md|config\.example\.yaml|deploy/helm/tg-antispam/values\.yaml|docs/architecture\.md|cmd/tg-antispam/[a-z0-9_]+\.go|internal/(telegram|telegram/fake|config|store|watch)/[a-z0-9_]+\.go|internal/config/testdata/[a-z0-9_]+\.yaml)$" "$f" && test -z "$(git status --porcelain -- . ":(exclude)report.json" ":(exclude)report-blocked.md" ":(exclude)tmp")"'`
- **AC-011.** Ревью-дифф влезает в потолок ревьюеров:
  `bash -c 'n=$(git diff 625b727a6bf3f6d4279f59b5a27fa785700f4d07..HEAD | wc -c) && test "$n" -gt 0 && test "$n" -le 95000'`

## 7. Контракт отчёта

`report.json` в корне клона, схемы v2, untracked. Имена полей дословные:

```json
{"schema_version": 2,
 "policy_id": "cross-review-v1",
 "handoff_status": "ready",
 "executor": {"backend": "grok", "model": "<точная модель>"},
 "spec_sha256": "<sha256 файла /home/deploy/github/telegram-antispam/docs/specs/captcha-m2a.md>",
 "base_sha": "625b727a6bf3f6d4279f59b5a27fa785700f4d07",
 "reviewed_sha": "<коммит, ушедший ревьюерам>",
 "final_sha": "<HEAD клона>",
 "review": {"resolutions": [], "initial_receipts": [], "verification_receipts": []},
 "criteria": [{"id": "AC-001", "status": "pass|fail|blocked",
               "command": "<команда-доказательство>", "rc": 0, "note": "…"}]}
```

- поле называется `reviewed_sha`, не `review_sha`; все три sha — 40 строчных hex, `final_sha` равен
  `HEAD` клона, цепочка `base_sha → reviewed_sha → final_sha` идёт от предка к потомку;
- `handoff_status` — `ready`, `blocked` или `needs_owner`;
- в `criteria` ровно одиннадцать записей AC-001…AC-011, `command` совпадает с командой критерия
  посимвольно, каждая перезапущена на `final_sha`;
- у `blocked` в `rc` стоит `null`, в `note` — дословная ошибка;
- в `review.initial_receipts` / `review.verification_receipts` — только имена файлов квитанций.

## 8. Контракт на невыполнимое

Спека противоречит себе, критерий невыполним или факт опровергает §2 — **остановись и доложи**:
`report-blocked.md` в корне клона с дословной командой, выводом и опровергнутым пунктом. Обходить
несовместимость запрещено: `--no-deps`, `GOTOOLCHAIN=local`, `|| true`, `set +e` в команде
критерия, `sudo`, `git push`, ослабление или удаление чужих тестов, `replace` в `go.mod`.

Отдельно:

- Реализация требует трогать пути из §4 — стоп, доложи, что и почему.
- Дифф не влезает в 95000 байт без потери обязательного — стоп, доложи размер по файлам
  (`git diff --stat`), не выкидывай обязательные тесты.
- Для любого пункта нужен живой Telegram — стоп.

## 9. Автономное перекрёстное ревью — обязательная часть вехи

Отчёт объявляет `policy_id: cross-review-v1`, приёмщик это проверяет. Без двух успешных квитанций
будет `blocked: initial: incomplete`.

1. Реализуй веху, прогони критерии, закоммить. `REVIEW_SHA` — `HEAD` после последнего рабочего
   коммита. Незакоммиченный код ревьюерам не передаётся.
2. Два ревью параллельно. Ты — Grok, пара: `codex` + `agy`.

   ```
   bash /home/deploy/.claude/skills/executor-milestone/scripts/review_run.sh initial <ревьюер> \
     --clone /home/deploy/exec-clones/antispam-captcha-m2a-20260926 \
     --base 625b727a6bf3f6d4279f59b5a27fa785700f4d07 \
     --range 625b727a6bf3f6d4279f59b5a27fa785700f4d07..<REVIEW_SHA> \
     --context "tg-antispam M2a: капча-кнопка на входе (мут, эфемерная кнопка + запасное сообщение, дедлайны в SQLite, sweep, kick|keep_muted) и отправка приветствия вне задачи секвенсора; спека /home/deploy/github/telegram-antispam/docs/specs/captcha-m2a.md; только чтение; tmux и процессы — только Docker с фейками"
   ```

3. `rc=0` ревью не доказывает. Отказ — пустой или оборванный ответ, ошибка квоты, нет строки
   вердикта. Вердикт — последнее вхождение `ВЕРДИКТ:` с `ПРИНЯТО` или `НЕ ПРИНИМАТЬ`. При временном
   сбое — один технический повтор; пустой stdout с `rc=124` — один повтор с поднятым таймаутом.
   Исчерпанная квота — `blocked`, без цикла повторов. Ревьюер недоступен и после повтора —
   `handoff_status: blocked` с дословной ошибкой; подменять ревьюера другим запрещено (это решает
   координатор).
4. Каждую находку перепроверь по коду. Подтверждённые исправь, затронутые критерии прогони заново,
   закоммить. Заход исправлений ОДИН.
5. **Завершающая проверка `verify` — ТОЛЬКО если по итогам ревью были правки** (`final_sha != reviewed_sha`):
   те же двое смотрят `<REVIEW_SHA>..<FINAL_SHA>` фазой `verify` той же командой. Правок не было —
   `FINAL_SHA == REVIEW_SHA`, `verify` не запускается, `verification_receipts` пуст, `handoff_status: ready`.
6. В `report.json`: `review.resolutions` — по записи на находку (`finding_id`, `decision` из
   `fixed|disproved|needs_owner`, `source_receipt`, `proof`, `fix_commits`); `fix_commits`
   обязателен при `fixed` и лежит в `reviewed_sha..final_sha`; у `disproved` в `proof` — команда и
   её вывод.

🚨 **После старта фазы `verify` не коммить ничего.**

## 10. Стыки с соседними вехами

- Координатор после приёмки: мутации по новым тестам (Codex), живой стенд с тестовым ботом в тестовой
  группе (эфемерная кнопка доходит? callback приходит? мут/снятие/кик), затем M2b и релиз 0.18.0.
- M2b (`join_request`) расширит `mode`, добавит `chat_join_request` в `allowed_updates` и
  `approve/declineChatJoinRequest`; опирается на таблицу `captcha_challenges`, `Sweep` и маршрут
  callback'ов этой вехи.
