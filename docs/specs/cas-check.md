# Веха: CAS — поштучная проверка /check вместо снятой выгрузки export.csv

- Репозиторий `/home/deploy/github/telegram-antispam`, дата 05.10.2026.
- BASE: `3bc9efc6bfb0e4c6b4ce2dcbb3b308b5303158a9` («Release version 0.19.1», `main`). Клон снят ровно с BASE.
  Спека в клоне не лежит: читай её по абсолютному пути
  `/home/deploy/github/telegram-antispam/docs/specs/cas-check.md`; в клон её не копируй и не коммить.
- Исполнитель: `cx` (Codex). Ревью и мутации проводит постановщик после приёмки — НЕ часть вехи:
  ревьюеров сам не запускай.
- Критериев: 8.

## 0. Где работать

Клон `/home/deploy/exec-clones/antispam-cas-check-20261005`, ветка `cas-check`. Живое дерево
`/home/deploy/github/telegram-antispam` не трогать. **push в origin запрещён** — работу заберёт
постановщик через `git fetch` из клона (push-url и так `no-push`). `git add` только по именам, никогда
`-A`. `.gopath/` (кэш модулей) постановщик положил в клон до запуска. `tmp/` — для своих логов, не
коммитить. До первой правки прочитай `AGENTS.md`, весь `internal/blocklist/` (код и тесты),
`internal/config/config.go` (тип `Blocklist`, `applyBlocklistDefaults`), `internal/config/config_test.go`
(`TestBlocklistDefaultsAppliedWhenUnset`, `TestBlocklistExplicitValuesNotOverridden`),
`cmd/tg-antispam/main.go` (блок «Blocklist mirror», ~строки 688–735), `internal/detect/cascade.go`
(`BlocklistSource`, стадия blocklist в `Decide`), `internal/ops/metrics.go` (`IncCounter`).

## 1. Задача и почему

CAS (cas.chat) снял массовую выгрузку: `GET https://api.cas.chat/export.csv` отвечает
`404 {"ok":false,"error_code":404,"description":"CSV exports are not supported anymore, please use /check"}`.
В проде под каждые 6 ч пишет `blocklist bootstrap: cas: … status 404`, CAS-часть блоклиста пуста
(fail-open, работает только LOLS). Решение владельца: убрать источник `export.csv` целиком и проверять
пользователей поштучно через `GET https://api.cas.chat/check?user_id=<id>` с кэшем, таймаутом, лимитом
частоты и fail-open при недоступности CAS. Положительный ответ CAS даёт ту же санкцию, что сейчас даёт
попадание в блоклист (сигнал `blocklist` каскада; пропуск приветствия/капчи) — новой ветки санкций нет.

## 2. Что проверено постановщиком 05.10.2026, а что предположение

Проверено вживую и чтением кода BASE:

- **П1.** `export.csv` (и на `api.cas.chat`, и на `cas.chat`) → 404 с текстом выше. Документация
  `https://cas.chat/api` описывает только метод `check` (единственный параметр `user_id`, `api_key`
  необязателен; без ключа работает).
- **П2.** Формат `/check` (живые запросы): чистый пользователь —
  HTTP 200, `{"ok":false,"description":"Record not found."}`; нечисловой или отсутствующий `user_id` —
  HTTP 200, `{"ok":false,"error":"Record not found."}`; забаненный (id 94398985 на момент проверки) —
  HTTP 200, `{"ok":true,"result":{"reasons":[1],"offenses":1,"messages":["…"],"time_added":"…"}}`.
  `content-type: application/json`. Правило: listed ⇔ HTTP 2xx и JSON-поле `ok == true`.
- **П3.** `blocklist.Blocklist.Listed(int64) bool` — единственная точка поиска; её зовут через
  интерфейс `detect.BlocklistSource{ Listed(userID int64) bool }`: каскад (`cascade.go` ~159, для всех
  не-админов, независимо от trust), `watch.Welcomer` (`welcome.go` ~92), `watch.Captcha`
  (`captcha.go` ~227, `captcha_join_request.go` ~22). В `main.go` один `blocklistSource` раздаётся всем
  четырём местам; при `blocklist.enabled: false` он остаётся nil-интерфейсом (типизированный nil
  запрещён — см. комментарий там же).
- **П4.** `RefreshFull` (`sync.go`) тянет два источника, `lols` и `cas` (`b.cfg.CasFullURL`), хранит
  last-good по источникам (`lolsFull`, `casFull`, `lolsDelta`); пустой ответ = сбой. `CasFullURL` живёт в
  `blocklist.Config`, `config.Blocklist` (`yaml:"cas_full_url"`, дефолт в `applyBlocklistDefaults`),
  `main.go`, `config.example.yaml`, тестах `sync_test.go` и `config_test.go` (~283).
- **П5.** Неизвестный ключ YAML не валит старт: `config.UnknownKeys` только предупреждает. Прод
  (`romtk3s op1/tg-antispam/values.yaml`) секцию `blocklist` не задаёт — работает на дефолтах.
- **П6.** Метрики: `(*ops.Registry).IncCounter(name, delta, labels...)`; в `main.go` уже есть образец
  колбэка-счётчика (`watch.Captcha.Count`).
- **П7.** `golang.org/x/time/rate` уже в `go.mod` (используется в `internal/queue`).
- **П8.** На BASE зелёный `./scripts/dev.sh test -race -count=1 ./...`.

Предположения (не проверены): лимитов частоты CAS на `/check` в документации нет; выбранные 10 rps —
наша вежливость, а не их требование.

## 3. Что сделать

### 3.1 Снять источник export.csv

- `internal/blocklist`: убрать `CasFullURL` из `Config`, поле `casFull` и всю CAS-ветку `RefreshFull`
  (полный список — только LOLS; правило «пустой ответ = сбой, last-good сохраняется» остаётся;
  `RefreshDelta` по-прежнему объединяет `lolsFull` и `lolsDelta`). Комментарии про CAS-список поправить.
- `internal/config`: убрать поле `CasFullURL` (`cas_full_url`) и его дефолт. Ключ в старых конфигах
  станет «неизвестным» (только предупреждение, П5) — это допустимо.
- `cmd/tg-antispam/main.go`: убрать передачу `CasFullURL`.

### 3.2 Новый файл `internal/blocklist/cas.go`

```go
type Source interface{ Listed(userID int64) bool }

// AnyOf: Listed = true, если хоть один источник true; опрос ПО ПОРЯДКУ с
// коротким замыканием (первый true — остальные не зовутся). nil-элементы пропускаются.
func AnyOf(srcs ...Source) Source

type CASConfig struct {
    URL         string        // "https://api.cas.chat/check"
    PositiveTTL time.Duration // 24h
    NegativeTTL time.Duration // 6h
    Timeout     time.Duration // 2s — бюджет одного запроса целиком
    RatePerSec  float64       // 10
    Burst       int           // 20
    MaxEntries  int           // 100000
}

type CASChecker struct {
    Now   func() time.Time   // nil → time.Now; часы для TTL, лимитера и брейкера
    Count func(result string) // nil → не считать
    // …
}

func NewCASChecker(cfg CASConfig, client *http.Client) *CASChecker // client nil → свой с Timeout
func (c *CASChecker) Listed(userID int64) bool
```

`NewCASChecker` подставляет дефолт из комментария вместо нулевого/отрицательного поля `CASConfig`.

Поведение `Listed` (строго в этом порядке):

1. `userID <= 0` → `false`, без запроса и без счёта.
2. Кэш: запись не истекла (`now < expires`) → её значение, без запроса и без счёта.
3. Брейкер открыт (`now < openUntil`) → `false`, `Count("breaker_open")`, без запроса.
4. Лимитер (`rate.Limiter.AllowN(now, 1)`, ждать нельзя) отказал → `false`, `Count("rate_limited")`.
5. Запрос `GET <URL>?user_id=<id>` (ровно один параметр, без `api_key`), контекст
   `context.WithTimeout(context.Background(), Timeout)`, заголовок `User-Agent` — та же строка, что в
   `FetchIDs`. Тело читается через `io.LimitReader` (1 MiB), разбирается только поле `ok`.
   - 2xx и `ok == true` → кэш `true` на `PositiveTTL`, `Count("listed")`, вернуть `true`;
   - 2xx и `ok == false` (любое описание) → кэш `false` на `NegativeTTL`, `Count("clean")`, вернуть `false`;
   - всё прочее (сеть, таймаут, не-2xx, невалидный JSON, нет поля `ok`) → ошибка: НЕ кэшировать,
     `Count("error")`, вернуть `false` (fail-open, без санкции).
6. Брейкер: 3 ошибки подряд → открыт на 60 с (`log.Printf` один раз при открытии, с последней ошибкой);
   любой успешный ответ (listed/clean) обнуляет счётчик; если до этого брейкер открывался — один
   `log.Printf` о восстановлении. После истечения 60 с следующий запрос идёт как обычно (одна ошибка —
   снова открыт на 60 с). Отдельных логов на каждую ошибку нет.
7. Кэш ограничен `MaxEntries`: при вставке в полный кэш сначала удалить истёкшие записи; если места
   всё равно нет — результат вернуть, но не кэшировать. Всё потокобезопасно (`-race`).

Константы брейкера (3, 60 с) — неэкспортируемые константы пакета.

### 3.3 Конфиг (`internal/config/config.go`, секция `blocklist`)

Новые поля и дефолты в `applyBlocklistDefaults` (по образцу соседних: `*bool` — дефолт только при nil;
числа и длительности `<= 0` → дефолт):

| yaml | тип | дефолт |
|---|---|---|
| `cas_check_enabled` | `*bool` | `true` |
| `cas_check_url` | `string` | `"https://api.cas.chat/check"` |
| `cas_positive_ttl` | `Duration` | `24h` |
| `cas_negative_ttl` | `Duration` | `6h` |
| `cas_timeout` | `Duration` | `2s` |
| `cas_rate_per_sec` | `float64` | `10` |
| `cas_burst` | `int` | `20` |

`MaxEntries` в конфиг не выносится (в `main.go` — 100000).

### 3.4 Связка (`cmd/tg-antispam/main.go`)

Внутри `if *cfg.Blocklist.Enabled`: при `*cfg.Blocklist.CasCheckEnabled` создать `CASChecker` из
конфига, `Count` → `reg.IncCounter("tg_antispam_cas_check_total", 1, "result", result)`, и
`blocklistSource = blocklist.AnyOf(bl, cas)` (снимок LOLS первым — попадание в LOLS не тратит запрос
к CAS); иначе `blocklistSource = bl` как сейчас. При выключенном блоклисте `blocklistSource` остаётся
nil-интерфейсом. Гейдж `tg_antispam_blocklist_size` не трогать (он про снимок LOLS).

### 3.5 Документация

- `config.example.yaml`: убрать `cas_full_url`, описать семь новых ключей с дефолтами, поправить
  описание секции.
- `README.md` (строки про CAS + LOLS и `internal/blocklist`) и `docs/architecture.md` (абзац про
  last-good источников ~261) — привести к новой схеме: LOLS — снимок, CAS — поштучная проверка с
  кэшем, fail-open.
- `CHANGELOG.md`, `## [Unreleased]`: `### Changed` — CAS проверяется поштучно через `/check` (кэш 24 ч /
  6 ч, таймаут 2 с, 10 rps, fail-open, метрика `tg_antispam_cas_check_total{result}`); `### Removed` —
  `cas_full_url` и загрузка `export.csv` (CAS выгрузку закрыл, 404). Версию не бампать.

### 3.6 Тесты

Новый файл `internal/blocklist/cas_test.go`, только `httptest.Server`, никакой реальной сети; время —
через `Now`. Имена обязательны; каждый тест проверяет и возвращаемое значение, и число запросов к
серверу, и вызовы `Count`, где это применимо:

- `TestCASCheckListed` — ответ `ok:true` → `true`, `Count("listed")`; сервер видел путь из `URL`,
  `user_id=<id>`, ровно один query-параметр, непустой `User-Agent`.
- `TestCASCheckClean` — `{"ok":false,"description":"Record not found."}` → `false`, `Count("clean")`.
- `TestCASCheckCachesWithinTTL` — положительный: повтор до `PositiveTTL` не ходит на сервер, после —
  ходит; отрицательный: то же для `NegativeTTL`. TTL в тесте разные (например 24h/6h), граница проверяется
  с обеих сторон.
- `TestCASCheckErrorsFailOpenUncached` — HTTP 500, невалидный JSON, JSON без `ok`: каждый → `false`,
  `Count("error")`, повторный вызов снова идёт на сервер.
- `TestCASCheckTimeout` — сервер держит ответ дольше `Timeout` (например 50 мс при 3 с ожидания):
  `false`, `Count("error")`, `Listed` вернулся не позже чем через `Timeout` + 1 с.
- `TestCASCheckRateLimited` — `RatePerSec: 1, Burst: 2`, часы стоят: третий разный id за тот же миг →
  `false`, `Count("rate_limited")`, сервер видел ровно 2 запроса; после сдвига часов на 1 с — снова ходит.
- `TestCASCheckBreaker` — 3 ошибки подряд → четвёртый вызов `false`, `Count("breaker_open")`, сервер не
  видел запроса; через 60 с запрос идёт; успешный ответ сбрасывает счётчик (после него две ошибки брейкер
  не открывают).
- `TestCASCheckZeroUserNoRequest` — `0` и отрицательный id: `false`, ноль запросов, ноль `Count`.
- `TestCASCheckCacheBounded` — `MaxEntries: 2`: третий id не кэшируется (повтор снова ходит на
  сервер), а после истечения TTL первых двух — кэшируется.
- `TestCASCheckConcurrent` — 50 горутин × разные/одинаковые id под `-race`, без паники и гонок, результаты
  верные.
- `TestAnyOfShortCircuits` — первый источник `true` → второй не вызван; первый `false` → второй вызван;
  nil-элемент пропускается; все `false` → `false`.

Новый файл `internal/config/cas_config_test.go`:

- `TestBlocklistCASDefaults` — пустая секция → семь дефолтов из §3.3; явный `cas_check_enabled: false`
  сохраняется; явные значения не перетираются; `0`/отрицательные длительности и числа → дефолт.

Существующие тесты: разрешено менять ТОЛЬКО `internal/blocklist/sync_test.go` (убрать CAS-источник:
тесты про независимый last-good двух источников переписать на LOLS full + LOLS delta либо удалить, если
смысл исчез — в отчёте AC-003 перечислить удалённые поимённо с причиной) и в
`internal/config/config_test.go` убрать проверку `CasFullURL` (~283). Остальные `*_test.go` — ни строки.

## 4. Не трогать

- Всё вне: `internal/blocklist/{blocklist.go,sync.go,cas.go,cas_test.go,sync_test.go}`,
  `internal/config/{config.go,config_test.go,cas_config_test.go}`, `cmd/tg-antispam/main.go`,
  `config.example.yaml`, `README.md`, `docs/architecture.md`, `CHANGELOG.md` (AC-005).
  В частности `internal/detect`, `internal/watch`, `internal/telegram`, `internal/store`, `deploy/`,
  `.github/`, `go.mod`, `go.sum`.
- Интерфейс `detect.BlocklistSource` и сигнатура `Listed` — без изменений.
- Релиз (бамп `Chart.yaml`, тег) — не делать. Живой Telegram, прод, k8s, токены, host tmux, сигналы
  чужим процессам, реальные запросы к cas.chat из тестов.

## 5. Разрешения

Сеть — только модульный прокси через `./scripts/dev.sh` (новых модулей нет). Docker — `./scripts/dev.sh`
(golang:1.26.6 под `--user $(id -u):$(id -g)` = 1002:1002) и `golang:1.26.6` для `gofmt`. Дифф
`BASE..HEAD` ≤ 70000 байт (AC-007). Коммиты: ≤50 символов, без подписей и Co-Authored-By.

## 6. Критерии приёмки

- **AC-001.** Полный сьют с гонками зелёный:
  `bash -c './scripts/dev.sh test -race -count=1 ./...'`
- **AC-002.** vet, сборка и gofmt чистые:
  `bash -c './scripts/dev.sh vet ./... && ./scripts/dev.sh build ./... && test -z "$(docker run --rm -u "$(id -u):$(id -g)" -v "$PWD":/src -w /src golang:1.26.6 gofmt -l cmd internal)"'`
- **AC-003.** Новые тесты проходят (поимённо):
  `bash -c 'out=$(./scripts/dev.sh test -race -count=1 -v -run "^(TestCASCheckListed|TestCASCheckClean|TestCASCheckCachesWithinTTL|TestCASCheckErrorsFailOpenUncached|TestCASCheckTimeout|TestCASCheckRateLimited|TestCASCheckBreaker|TestCASCheckZeroUserNoRequest|TestCASCheckCacheBounded|TestCASCheckConcurrent|TestAnyOfShortCircuits|TestBlocklistCASDefaults)$" ./internal/blocklist/ ./internal/config/ 2>&1) && for t in TestCASCheckListed TestCASCheckClean TestCASCheckCachesWithinTTL TestCASCheckErrorsFailOpenUncached TestCASCheckTimeout TestCASCheckRateLimited TestCASCheckBreaker TestCASCheckZeroUserNoRequest TestCASCheckCacheBounded TestCASCheckConcurrent TestAnyOfShortCircuits TestBlocklistCASDefaults; do printf "%s\n" "$out" | grep -Eq -- "^--- PASS: $t( |$)" || exit 1; done'`
- **AC-004.** Прочие существующие тесты не изменены, удалены или переименованы:
  `bash -c 'test -z "$(git diff --name-only --diff-filter=MDR 3bc9efc6bfb0e4c6b4ce2dcbb3b308b5303158a9..HEAD -- "*_test.go" | grep -vE "^internal/(blocklist/sync_test|config/config_test)\.go$")"'`
- **AC-005.** Состав работы в границах, дерево чистое:
  `bash -c 'f=$(mktemp) && git diff --name-only 3bc9efc6bfb0e4c6b4ce2dcbb3b308b5303158a9..HEAD > "$f" && ! grep -qvE "^(CHANGELOG\.md|README\.md|config\.example\.yaml|docs/architecture\.md|cmd/tg-antispam/main\.go|internal/blocklist/(blocklist|sync|cas|cas_test|sync_test)\.go|internal/config/(config|config_test|cas_config_test)\.go)$" "$f" && test -z "$(git status --porcelain -- . ":(exclude)report.json" ":(exclude)report-blocked.md" ":(exclude)tmp" ":(exclude).gopath")"'`
- **AC-006.** Следов export.csv в коде и конфиге нет, CAS-проверка подключена:
  `bash -c '! git grep -nE "export\.csv|CasFullURL|cas_full_url|casFull" -- cmd internal config.example.yaml && git grep -q "blocklist.AnyOf(" -- cmd/tg-antispam/main.go && git grep -q "tg_antispam_cas_check_total" -- cmd/tg-antispam/main.go'`
- **AC-007.** Дифф влезает в потолок:
  `bash -c 'n=$(git diff 3bc9efc6bfb0e4c6b4ce2dcbb3b308b5303158a9..HEAD | wc -c) && test "$n" -gt 0 && test "$n" -le 70000'`
- **AC-008.** CHANGELOG — запись в Unreleased, деплой не тронут:
  `bash -c 'sed -n "/^## \[Unreleased\]/,/^## \[0/p" CHANGELOG.md | grep -q "^### Removed" && sed -n "/^## \[Unreleased\]/,/^## \[0/p" CHANGELOG.md | grep -q "/check" && git diff --quiet 3bc9efc6bfb0e4c6b4ce2dcbb3b308b5303158a9..HEAD -- deploy'`

## 7. Контракт отчёта

`report.json` в корне клона, untracked:

```json
{"criteria": [{"id": "AC-001", "status": "pass|fail|blocked",
               "command": "<команда из §6 посимвольно>", "rc": 0, "note": "…"}]}
```

Ровно восемь записей AC-001…AC-008, каждая перезапущена на финальном HEAD; у `blocked` `rc: null` и
дословная ошибка в `note`. В `note` AC-003 — список удалённых/переписанных тестов `sync_test.go` с
причиной. Работа закоммичена (HEAD клона ≠ BASE).

## 8. Контракт на невыполнимое

Спека противоречит себе, факт §2 опровергнут, существующий тест вне разрешённых двух файлов падает от
новой семантики, нужно трогать пути §4 или реальную сеть — **стоп**: `report-blocked.md` с дословной
командой, выводом и пунктом спеки. Обходить несовместимость запрещено: `--no-deps`,
`GOTOOLCHAIN=local`, `|| true`, `set +e` в критерии, `sudo`, `git push`, ослабление/удаление чужих
тестов, `replace` в `go.mod`.

## 9. Авторевью

В этой вехе авторевью исполнитель НЕ запускает: перекрёстное ревью диффа (Codex и agy) и мутации проводит
координатор после приёмки, исправления по ним — отдельным заходом.

## 10. Стыки

Координатор после приёмки: Codex- и agy-ревью, мутации по новым тестам (Grok), релиз 0.20.0 (бамп
`Chart.yaml`, тег, `romtk3s`), проверка на op1 (нет 404 CAS, счётчик проверок растёт).
