# Веха: тесты против 13 выживших мутантов welcome-m1

- Репозиторий `/home/deploy/github/telegram-antispam` (публичный `stufently/telegram-antispam`),
  дата 24.09.2026.
- BASE_SHA: `0a2c9fe004467104465c727cd95979d7cf68ff24` («Count welcome attempts when they finish»,
  голова принятой по коду ветки `welcome-m1`). Клон снимается с коммита спеки, который лежит сразу
  над BASE и добавляет только эту спеку, чекер `docs/specs/checks/welcome_m1_test_gaps_check.py` и
  пункт в `docs/spec-queue.md`. BASE — левая граница ревью-диффа и значение `base_sha` в отчёте.
- Исполнитель: `cx` (Codex). Код вехи M1 писал другой исполнитель, мутации по нему гонял Codex;
  эта веха пишет только тесты. Исправления по ревью — тот же исполнитель.
- Критериев: 5.
- Решение владельца 24.09.2026: сначала закрыть дыры в тестах, потом мерж `welcome-m1` в `main`.

## 0. Где работать

Одноразовый клон `/home/deploy/exec-clones/telegram-antispam-welcome-testgaps-20260924`, ветка
`welcome-m1` (уже выбрана, новую не заводить). Живое дерево `/home/deploy/github/telegram-antispam`
и клон `/home/deploy/exec-clones/telegram-antispam-welcome-m1-20260924` не трогать. **Push в origin
запрещён: работу заберёт постановщик через `git fetch` из клона.** Коммиты локальные, `git add`
только по именам файлов, никогда `-A`. Прочитай `AGENTS.md` до первой правки.

### Что кладёт постановщик до запуска

| Что | Как | Коммитить? |
|---|---|---|
| эта спека, чекер, `docs/spec-queue.md` | уже в коммите клона | уже закоммичены, не менять |
| кэш модулей `.gopath/` | `./scripts/dev.sh mod download` в клоне | нет, `.gopath/` в `.gitignore` |
| образы `golang:1.26.6`, `golangci/golangci-lint:v2.12.2` | уже на хосте | — |

Go на хосте не используется: всё через `./scripts/dev.sh` (Docker под твоим uid). Каталог `tmp/`
в клоне — для улик ревью, не коммитить.

## 1. Задача и почему

Мутационный прогон по новым тестам M1 (`/home/deploy/exec-clones/telegram-antispam-welcome-m1-mut-20260924/mutation-report.md`,
полные диффы — `mutation-report.json` там же): 78 мутантов, 13 выжили. Все 13 — дыры в ТЕСТАХ,
прод-код правильный. Нужно дописать тесты так, чтобы каждый выживший убивался своим НОВЫМ тестом,
плюс одна косметическая правка комментария. Прод-логика не меняется.

| Мутант | Место | Мутация | Новый тест (имя дословно) | Файл теста |
|---|---|---|---|---|
| M10 | `internal/telegram/livept.go` `sendToUser` | ошибка удаления оборачивается `%v` вместо `%w` | `TestSendEphemeralWrapsDeleteError` | `internal/telegram/ephemeral_test.go` |
| M78 | там же | удаляется `messageID + 100` вместо `messageID` | `TestSendEphemeralDeletesExactFallbackID` | `internal/telegram/ephemeral_test.go` |
| M17 | `internal/telegram/join.go` | снята проверка `user.ID == 0` | `TestJoinRejectsZeroUserID` | `internal/telegram/join_test.go` |
| M30 | `internal/config/config.go` `Welcome.For` | общий текст без `TrimSpace` | `TestWelcomeForTrimsGlobalText` | `internal/config/welcome_test.go` |
| M38 | `internal/config/config.go` `validateWelcome` | длина текста чата `>` → `>=` | `TestWelcomeChatTextAtLimit` | `internal/config/welcome_test.go` |
| M49 | `internal/store/migrate.go` | у `welcome_sent` нет `PRIMARY KEY` | `TestWelcomedRepeatMarkKeepsOneRow` | `internal/store/welcome_test.go` |
| M62 | `internal/watch/welcome.go` | окно лимита 60 → 59 с | `TestWelcomerRateCapInsideWindow` | `internal/watch/welcome_test.go` |
| M63 | там же | окно лимита 60 → 61 с | `TestWelcomerRateCapWindowEdge` | `internal/watch/welcome_test.go` |
| M64 | там же, `prune` | `ts.After(cutoff)` → `!ts.Before(cutoff)` | `TestWelcomerRateCapWindowEdge` | `internal/watch/welcome_test.go` |
| M72 | `Welcomer.Observe` | шаги «зарегистрирован» ↔ «включено» | `TestWelcomerNotAdmittedBeforeDisabled` | `internal/watch/welcome_test.go` |
| M73 | там же | шаги «включено» ↔ «строка чата» | `TestWelcomerDisabledBeforeChatRow` | `internal/watch/welcome_test.go` |
| M74 | там же | шаги «строка чата» ↔ «блоклист» | `TestWelcomerChatRowBeforeBlocklist` | `internal/watch/welcome_test.go` |
| M76 | там же | лимит проверяется раньше «уже знаком» | `TestWelcomerKnownBeforeRateCap` | `internal/watch/welcome_test.go` |

Точные фрагменты мутаций — словарь `MUTATIONS` в чекере; соответствие «мутант → тест» — `KILLERS`.

## 2. Что проверено вживую, а что предположение

Проверено постановщиком 24.09.2026 чтением кода на BASE:

- Каждый фрагмент из `MUTATIONS` встречается в своём файле ровно один раз (чекер проверяет это
  на каждом прогоне), фрагменты получены наложением диффов из `mutation-report.json` на BASE.
- `sendToUser` (`livept.go` ~650–685): при ответе с `message_id` и без `ephemeral_message_id` зовёт
  `p.DeleteMessages(ctx, chat, []int{sent.messageID})`; при ошибке возвращает
  `fmt.Errorf("%w: %w", ErrEphemeralNotHonored, delErr)`. `DeleteMessages` пропускает ошибку через
  `mapRetry` и `ignoreAlreadyGone` без переобёртки (глотается только «message to delete not found»),
  `submitSync` отдаёт ошибку как есть. go-telegram/bot v1.27.0 на HTTP 400 без
  `migrate_to_chat_id` возвращает `fmt.Errorf("%w, %s", ErrorBadRequest, description)`
  (`raw_request.go`), значит на исходном коде `errors.Is(err, tgbot.ErrorBadRequest)` истинно.
- В `TestSendEphemeralDeletesPublicFallback` поле формы `message_ids` берётся сырой строкой
  (`f["message_ids"]`), проверка — `strings.Contains(gotDelete, "77")`. Хелперы
  `startLivePort`, `formOf`, `writeJSON` лежат в `ephemeral_test.go`, пакет `telegram`,
  `tgbot` — импорт `github.com/go-telegram/bot`.
- `JoinFromChatMemberUpdated` отбрасывает `user.ID == 0` отдельным условием после `incomingUser`;
  хелперы `asLeft`, `asMember`, `asRestricted` в `join_test.go`.
- `Welcome.For` тримит общий текст (`strings.TrimSpace(w.Text)`), текст чата тримит отдельно;
  `validateWelcome` режет текст чата при `n > telegramMessageRunes` (4096). Тесты конфига зовут
  `Parse([]byte(yaml))`, база YAML — как в `TestWelcomeValidate`.
- `store.DB` даёт `Read() *sql.DB` (`engine.go`), хелпер `newMigrated(t)` — в `chats_test.go`;
  «старая» БД в `TestWelcomedRoundTrip` мигрирует той же схемой (`CREATE TABLE IF NOT EXISTS`).
- `Welcomer.Observe` — порядок: `RegisteredChat` → `Welcome.For` → `Store.GetChat` (строка
  `Enabled=false` → `skip_chat_disabled`) → блоклист → `WasWelcomed` → `TrustCount` → лимит →
  `SendWelcome`. Окно — `welcomeWindow = 60 * time.Second`, в окне остаются отметки
  `ts.After(now - 60s)`, отметка ставится `w.now()` ПОСЛЕ отправки. Тестовые хелперы в
  `welcome_test.go` пакета `watch`: `memWelcome`, `listedIDs`, `welcomeCfg`, `onWelcome`,
  `disabledRow`, `sends`, `newW`, `boolPtr`, `intPtr`; `w.Now` подменяется замыканием.
- Выполнимость по каждому мутанту выведена из кода (что именно наблюдаемо меняется — см. §3); это
  анализ, а не прогон набросков тестов. **Предположение:** ни один из 13 не эквивалентен.
- Чистый клон: `./scripts/dev.sh vet ./...`, `gofmt -l cmd internal` (пусто), `golangci-lint run ./...`
  (`0 issues.`) — зелёные. Критерий состава (AC-005) на свежем клоне зелёный; AC-001 и AC-004
  красные по задуманной причине: «no single `func TestSendEphemeralWrapsDeleteError(`…» и
  «applyLLMDefaults is not directly preceded by its comment».

## 3. Что сделать

1. Дописать 12 новых тестовых функций (M63 и M64 убивает одна) с именами ДОСЛОВНО из таблицы, в
   КОНЕЦ названных файлов. Что проверяет каждая:
   - **M10** — `deleteMessages` отвечает `{"ok":false,"error_code":400,"description":"Bad Request: message can't be deleted"}`;
     ошибка `SendEphemeral` обязана удовлетворять И `errors.Is(err, ErrEphemeralNotHonored)`, И
     `errors.Is(err, tgbot.ErrorBadRequest)`.
   - **M78** — `sendMessage` возвращает `message_id` 77 без эфемерного id; `message_ids` запроса
     `deleteMessages` декодируется JSON в `[]int` и равен ровно `[]int{77}`.
   - **M17** — вход `left → member` и `left → restricted(IsMember=true)` с `models.User{ID: 0}`:
     `ok == false` и событие `== JoinEvent{}`.
   - **M30** — включённый `Welcome` с `Text: " \tобщий\n "` без записи чата: `For(id)` возвращает
     `("общий", true)`.
   - **M38** — `Parse` конфига `welcome.enabled: false`, `welcome.chats[-100]: {enabled: true, text: <ровно 4096 рун>}`
     проходит без ошибки, `For(-100)` возвращает весь текст и `true`.
   - **M49** — после двух `MarkWelcomed(-100, 7)` запрос `SELECT COUNT(*) FROM welcome_sent WHERE chat_id=-100 AND user_id=7`
     через `db.Read()` даёт ровно 1 — на свежей (`newMigrated`) и на мигрированной старой БД
     (как во второй половине `TestWelcomedRoundTrip`).
   - **M62** — `max_per_minute: 1`, отправка в `t0`; новый пользователь в `t0+59.5s` →
     `skip_rate_capped`, отправок по-прежнему 1.
   - **M63, M64** — `max_per_minute: 1`, отправка в `t0`; новый пользователь ровно в `t0+60s` →
     `sent`, отправок 2.
   - **M72** — чат вне allowlist при `welcome.enabled=false` → `skip_not_admitted`, без отправки.
   - **M73** — `welcome.enabled=false` и строка чата `Enabled=false` (и отдельным случаем —
     `GetChat` с ошибкой) → `skip_disabled`, ошибка `nil`, без отправки.
   - **M74** — строка чата `Enabled=false` и пользователь в блоклисте → `skip_chat_disabled`, без отправки.
   - **M76** — лимит исчерпан успешной отправкой, в ту же минуту событие пользователя, который уже
     приветствован → `skip_known`, а не `skip_rate_capped`; то же для пользователя с `trust=1`.
     Новых отправок нет.
2. Каждый ассерт, валящий тест на мутанте, должен сообщаться строкой ВНУТРИ тела своей функции:
   `t.Fatalf` в теле или в `t.Run`-замыкании, либо хелпер с `t.Helper()`. Хелпер без `t.Helper()`
   сообщает свою строку, и чекер не засчитает мутанта.
3. Перенести doc-комментарий `applyLLMDefaults` в `internal/config/config.go`: сейчас три строки
   `// applyLLMDefaults fills LLM fields left unset…` стоят над комментарием
   `applyWelcomeDefaults`; поставить их без изменений прямо над `func (c *Config) applyLLMDefaults() {`.
   Больше в `config.go` ничего не менять.
4. Прогнать критерии, закоммитить (тесты и `config.go`), затем авторевью по §9.

## 4. Не трогать

- Прод-код: всё в `cmd/`, `internal/` кроме пяти тестовых файлов из таблицы и переноса
  комментария в `config.go`. Остальные тестовые файлы, `fake`, хелперы.
- Существующие строки пяти тестовых файлов: только ДОБАВЛЕННЫЕ строки (новые импорты — тоже
  добавление). Нужен свой хелпер — новая функция рядом с тестом.
- `CHANGELOG.md` (поведение не меняется), `go.mod`, `go.sum`, `docs/**` — включая эту спеку, чекер
  и `docs/spec-queue.md`, они уже закоммичены постановщиком.
- Мутацию, чекер и состав критериев не менять ради приёмки.

## 5. Разрешения

Разрешено: правки из §3, `./scripts/dev.sh` и `docker run` с образами из §0 под своим uid,
временные каталоги, коммиты в `welcome-m1` в клоне, запуск чекера, ревью по §9.
Запрещено: `git push`, выход за пределы клона, `sudo`, host Go, host tmux и чужие процессы,
живой Telegram.

## 6. Критерии приёмки

Чекер мутаций для каждого мутанта: проверяет, что тест новый (его не было на BASE) и один;
гоняет ТОЛЬКО этот тест на чистом дереве (обязан пройти); накладывает мутацию в прод-файл НА МЕСТЕ
(фрагмент ровно один), гоняет тот же тест (обязан упасть `--- FAIL` с ассертом внутри тела своей
функции, мутант обязан собраться); откатывает файл в `finally` и сверяет sha256. Прогоны
сериализуются `flock`, недоигранный откат восстанавливается при следующем старте. rc=0 — все
мутанты убиты. Для отладки одного: `python3 docs/specs/checks/welcome_m1_test_gaps_check.py mutants M62`.
Не запускай чекер мутаций параллельно с другими командами, читающими прод-файлы.

- **AC-001.** Каждый из 13 выживших мутантов убит своим новым тестом:
  `bash -c 'python3 docs/specs/checks/welcome_m1_test_gaps_check.py mutants'`
- **AC-002.** Полный сьют с гонками зелёный:
  `bash -c './scripts/dev.sh test -race -count=1 ./...'`
- **AC-003.** vet, gofmt и golangci-lint чистые:
  `bash -c './scripts/dev.sh vet ./... && test -z "$(docker run --rm -u "$(id -u):$(id -g)" -v "$PWD":/src -w /src golang:1.26.6 gofmt -l cmd internal)" && docker run --rm --network host -u "$(id -u):$(id -g)" -e HTTP_PROXY -e HTTPS_PROXY -e NO_PROXY -e GOFLAGS=-mod=mod -e GOPATH=/src/.gopath -e GOCACHE=/src/.gopath/cache -e GOLANGCI_LINT_CACHE=/src/.gopath/lintcache -v "$PWD":/src -w /src golangci/golangci-lint:v2.12.2 golangci-lint run ./...'`
- **AC-004.** Комментарий applyLLMDefaults над своей функцией, иных правок config.go нет:
  `bash -c 'python3 docs/specs/checks/welcome_m1_test_gaps_check.py comment'`
- **AC-005.** Состав работы в границах вехи, тесты только дописаны, дерево чистое:
  `bash -c 'python3 docs/specs/checks/welcome_m1_test_gaps_check.py scope'`

## 7. Контракт отчёта

`report.json` в корне клона, схемы v2, untracked. Имена полей дословные:

```json
{"schema_version": 2,
 "policy_id": "cross-review-v1",
 "handoff_status": "ready",
 "executor": {"backend": "codex", "model": "<точная модель>"},
 "spec_sha256": "<sha256 файла docs/specs/welcome-m1-test-gaps.md>",
 "base_sha": "0a2c9fe004467104465c727cd95979d7cf68ff24",
 "reviewed_sha": "<коммит, ушедший ревьюерам>",
 "final_sha": "<HEAD клона>",
 "review": {"resolutions": [], "initial_receipts": [], "verification_receipts": []},
 "criteria": [{"id": "AC-001", "status": "pass|fail|blocked",
               "command": "<команда-доказательство>", "rc": 0, "note": "…"}]}
```

- поле называется `reviewed_sha`, не `review_sha`; все три sha — 40 строчных hex, `final_sha` равен
  `HEAD` клона, цепочка `base_sha → reviewed_sha → final_sha` идёт от предка к потомку;
- `handoff_status` — `ready`, `blocked` или `needs_owner`;
- в `criteria` ровно пять записей AC-001…AC-005, `command` совпадает с командой критерия
  посимвольно, каждая перезапущена на `final_sha`;
- у `blocked` в `rc` стоит `null`, в `note` — дословная ошибка;
- в `review.initial_receipts` / `review.verification_receipts` — только имена файлов квитанций.

## 8. Контракт на невыполнимое

Спека противоречит себе, критерий невыполним или факт опровергает §2 — **остановись и доложи**:
`report-blocked.md` в корне клона с дословной командой, выводом и опровергнутым пунктом. Обходить
несовместимость запрещено: `--no-deps`, `GOTOOLCHAIN=local`, `|| true`, `set +e` в команде
критерия, `sudo`, `git push`, правка прод-кода, чекера или существующих тестов.

Отдельно:

- Мутанта нельзя убить тестом без правки прод-кода или существующих тестов, либо чекер считает
  мутацию не легшей (фрагмент не один раз) или не собравшейся — стоп, не меняй мутацию и чекер,
  доложи мутанта и вывод чекера. «Эквивалентный мутант» — только с письменным обоснованием,
  почему наблюдаемое поведение не меняется; «не смог убить» обоснованием не является.
- Существующий тест падает на чистом дереве — стоп, не правь его, доложи имя и вывод.

## 9. Автономное перекрёстное ревью — обязательная часть вехи

Отчёт объявляет `policy_id: cross-review-v1`, приёмщик это проверяет. Без двух успешных квитанций
будет `blocked: initial: incomplete`.

1. Реализуй веху, прогони критерии, закоммить. `REVIEW_SHA` — `HEAD` после последнего рабочего
   коммита. Незакоммиченный код ревьюерам не передаётся.
2. Два ревью параллельно. Пара для Codex: `agy` + `grok`.

   ```
   bash /home/deploy/.claude/skills/executor-milestone/scripts/review_run.sh initial <ревьюер> \
     --clone /home/deploy/exec-clones/telegram-antispam-welcome-testgaps-20260924 \
     --base 0a2c9fe004467104465c727cd95979d7cf68ff24 \
     --range 0a2c9fe004467104465c727cd95979d7cf68ff24..<REVIEW_SHA> \
     --context "tg-antispam welcome-m1 test gaps: 12 новых Go-тестов против 13 выживших мутантов + перенос комментария applyLLMDefaults; прод-логика не меняется; только чтение; tmux и процессы — только Docker с фейками"
   ```

3. `rc=0` ревью не доказывает. Отказ — пустой или оборванный ответ, ошибка квоты, нет строки
   вердикта. Вердикт — последнее вхождение `ВЕРДИКТ:` с `ПРИНЯТО` или `НЕ ПРИНИМАТЬ`. При временном
   сбое — один технический повтор; пустой stdout с `rc=124` — один повтор с `GROK_TIMEOUT=1500`.
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

- Координатор после приёмки забирает `welcome-m1` из клона и вливает в `main` (CI на push в `main`
  — решение основной сессии), затем релиз и живой стенд по плану M1.
- Отдельный мутационный прогон противоположным исполнителем по тестам этой вехи сверх чекера —
  на усмотрение координатора.
- Следующая веха — M2 (капча), пункт в `docs/spec-queue.md`; эта веха её не касается.
