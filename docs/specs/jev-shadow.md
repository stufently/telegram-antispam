# Веха: теневой режим LLM-провайдера, промпт на провайдера, метрики по провайдерам

- Репозиторий `/home/deploy/github/telegram-antispam`, дата 21.10.2026.
- BASE: `c784bb325103301cca92ebbd97bc15461249b0d7` («fix: require both lifts on unknown undo», `main`).
  Клон снят ровно с BASE. Спека в клоне не лежит: читай её по абсолютному пути
  `/home/deploy/github/telegram-antispam/docs/specs/jev-shadow.md`, в клон не копируй и не коммить.
- Исполнитель: `cx` (Codex) — выбор владельца. Ревью диффа и мутации проводит постановщик после
  приёмки — НЕ часть вехи: ревьюеров сам не запускай.
- Критериев: 8.

## 0. Где работать

Клон `/home/deploy/exec-clones/antispam-jev-shadow-20261021`, создай ветку `jev-shadow`. Живое дерево
`/home/deploy/github/telegram-antispam` не трогать. **push в origin запрещён** — работу заберёт
постановщик через `git fetch` из клона. `git add` только по именам, никогда `-A`. `.gopath/` (кэш
модулей, в `.gitignore`) постановщик положил в клон до запуска. `tmp/` — для своих логов, не коммитить.

До первой правки прочитай: `AGENTS.md`; весь `internal/llm/` (код и тесты); в
`internal/config/config.go` — `LLMProvider`, `LLM`, `PromptFor`, `applyLLMDefaults`, LLM-часть
`Validate`; `cmd/tg-antispam/llm_providers.go`; в `cmd/tg-antispam/main.go` — сборку `llmJudge`
(~строки 803–815) и замыкание `decideWith` (~848–910), `llmMessageText`, `hasBorderline`, `boolLabel`;
`internal/ops/metrics.go` (`IncCounter`, `Write`).

## 1. Задача и почему

В приложении есть провайдер `kind: jev` (TypeSafe System One), но включить его в бою безопасно нельзя:

1. При `policy: any` любой провайдер, сказавший spam, приводит к санкции (`delete_mute`). Непроверенный
   на наших чатах JEV наказывал бы в одиночку, а удалённое сообщение не восстановить.
2. `llm.prompt` и `prompt_overrides` общие для всех провайдеров; боевой промпт написан под OpenAI
   («Ответь РОВНО одним словом: SPAM или HAM») и подменяет у JEV его встроенный вопрос.
3. Метрики LLM не знают провайдера: нельзя понять, кто проголосовал spam, и оценить JEV.

Нужно: (а) **теневой провайдер** — вызывается на тех же сообщениях, его ответ пишется в лог и
метрики, но в решение, инцидент, обучение и существующие метрики не попадает и модерацию не
задерживает; (б) **промпт на провайдера** с явной проверкой, что исключения чатов не теряются молча;
(в) **метрики по провайдерам** и счётчик согласия тени с голосующими.

## 2. Что проверено постановщиком 21.10.2026, а что предположение

Проверено чтением кода BASE:

- **П1.** `llm.Judge{Providers, Policy}.Adjudicate(ctx, text, prompt) Outcome` (`internal/llm/llm.go`)
  зовёт всех провайдеров параллельно с ОДНИМ prompt; `Outcome{Spam, Failed, Total, Err}`; решение —
  `decide(policy, results)`; ответы, пришедшие до дедлайна, учитываются, неответившие — failures.
- **П2.** `Provider{ Name() string; Classify(ctx, text, prompt) (bool, error) }`. `Name()` у OpenAI —
  `"openai"`, Anthropic — `"anthropic"`, JEV — `"jev"`.
- **П3.** `JEV.Classify` (`internal/llm/jev.go`): prompt-аргумент > `j.Prompt` > встроенный
  `jevQuestion`; результат — `p >= j.Threshold`, где `p` — поле `answers.spam.noul` ответа.
- **П4.** `cmd/tg-antispam/llm_providers.go`: `llmProviders(cfg config.LLM) []llm.Provider` — порядок
  результата совпадает с `cfg.Providers`; всем провайдерам передаётся `Prompt: cfg.Prompt`.
- **П5.** `config.LLMProvider{Kind, APIKey, Model, APIBase, Threshold, MaxSymbols}`;
  `LLM.PromptFor(chatID)` = непустой `PromptOverrides[chatID]`, иначе `Prompt`. `applyLLMDefaults`
  ставит JEV `model`/`threshold`/`max_symbols` по умолчанию. `Validate` при `llm.enabled: true` требует
  непустой `providers`, `api_key`, `model`, корректные JEV-поля.
- **П6.** `main.go`: `llmJudge` строится при `*cfg.LLM.Enabled && len(cfg.LLM.Providers) > 0`;
  `BayesAlwaysBorderline` и вызов стадии зависят от `llmJudge != nil`. В `decideWith`:
  `llmJudge.Adjudicate(cctx, llmMessageText(m), cfg.LLM.PromptFor(m.ChatID))` с таймаутом
  `cfg.LLM.HTTPTimeout`; затем `tg_antispam_llm_provider_failures_total` (+Failed) и ОДНА выборка
  `tg_antispam_llm_checks_total{result=spam|ham|error}` (`error`, если `Failed >= Total`); при `Spam`
  вердикт становится `{Signals: [{Name: "llm"}], Reason: "llm"}`.
- **П7.** `domain.Message` содержит `ChatID int64` и `MessageID int`.
- **П8.** `(*ops.Registry).IncCounter(name, delta, labels...)`, `Write(io.Writer)` печатает
  Prometheus-текст — им можно читать значения в тестах.
- **П9.** На BASE зелёный `./scripts/dev.sh test -race -count=1 ./...` (golang:1.27.3).

Предположений, на которые опирается веха, нет: живые запросы к JEV в вехе не нужны.

## 3. Что сделать

### 3.1 Конфиг (`internal/config/config.go`)

В `LLMProvider` три новых поля:

| yaml | тип | смысл |
|---|---|---|
| `shadow` | `bool` | теневой провайдер: вызывается, но не голосует |
| `prompt` | `string` | промпт этого провайдера вместо `llm.prompt` |
| `prompt_overrides` | `map[int64]string` | промпт этого провайдера для конкретных чатов |

Новый метод:

```go
// ProviderPromptFor: первый непустой (после TrimSpace) из
// 1) Providers[i].PromptOverrides[chatID], 2) Providers[i].Prompt, 3) l.PromptFor(chatID).
// Пустая строка = провайдер берёт свой встроенный промпт (поведение BASE).
func (l LLM) ProviderPromptFor(i int, chatID int64) string
```

`Validate` при `llm.enabled: true` дополнительно:

1. Должен быть хотя бы один провайдер с `shadow: false`, иначе ошибка, в тексте которой есть слово
   `voting` (стадия без голосующих никогда бы не действовала).
2. Если у провайдера непустой `prompt`, то для КАЖДОГО ключа `llm.prompt_overrides` у этого провайдера
   обязан быть непустой `prompt_overrides[тот же chat id]`; иначе ошибка, содержащая индекс провайдера
   (`llm.providers[<i>]`) и десятичный chat id. Смысл: собственный промпт провайдера иначе молча
   перекрыл бы исключение чата. Провайдер без своего `prompt` этим правилом не проверяется.

### 3.2 Пакет `internal/llm`

**Голоса.** В `Outcome` добавить `Votes []Vote` — по одному на провайдера, В ПОРЯДКЕ `Providers`:

```go
type Vote struct {
    Provider string   // Provider.Name()
    Spam     bool
    OK       bool     // ответил без ошибки до дедлайна
    Err      error    // ошибка вызова; для неответившего к дедлайну — ctx.Err()
    Score    *float64 // только у Scorer (см. ниже), иначе nil
}
```

Новый метод `func (j Judge) AdjudicateEach(ctx context.Context, text string, prompts []string) Outcome`:
то же, что `Adjudicate`, но провайдер `i` получает `prompts[i]` (`len(prompts) != len(Providers)` —
каждому провайдеру пустая строка). `Adjudicate(ctx, text, prompt)` становится обёрткой над ним с одним
prompt для всех; поведение и поля `Spam/Failed/Total/Err` — ровно как на BASE (существующие тесты
пакета не меняются и остаются зелёными).

**Оценка JEV.** Интерфейс

```go
type Scorer interface {
    Provider
    Score(ctx context.Context, text, prompt string) (float64, error)
    Cutoff() float64 // порог, с которым Classify сравнивает Score (включительно)
}
```

`JEV` реализует его: `Score` — текущее тело `Classify` до сравнения (все проверки и ошибки те же),
`Cutoff()` возвращает `Threshold`, `Classify` = `Score` и `p >= Threshold`. Наблюдаемое поведение
`Classify` не меняется (`jev_test.go` не трогать, он остаётся зелёным).

**Тень.** Новый файл `internal/llm/shadow.go`:

```go
type Shadow struct {
    Providers []Provider
    Timeout   time.Duration // бюджет одного наблюдения целиком
    Max       int           // одновременных наблюдений; <= 0 → 1
}

// Observe запускает наблюдение в отдельной горутине и сразу возвращается.
// false — слот не взят (уже Max наблюдений в полёте): провайдеры НЕ вызываются,
// report не вызывается. true — горутина с контекстом WithTimeout(parent, Timeout)
// вызывает всех Providers параллельно (провайдер i получает prompts[i]; длина
// не совпала — пустые строки) и ровно один раз зовёт report с голосами в порядке
// Providers, когда ответили все или истёк Timeout (неответившие: OK=false,
// Err=ctx.Err()). Для Scorer вызывается ТОЛЬКО Score: Score заполнен,
// Spam = score >= Cutoff(). Слот освобождается после возврата report.
func (s *Shadow) Observe(parent context.Context, text string, prompts []string, report func([]Vote)) bool
```

Потокобезопасно (`-race`). Тень никогда не возвращает решение — только голоса в `report`.

### 3.3 Стадия LLM (`cmd/tg-antispam/llm_stage.go`, новый файл)

Вынести логику LLM из `decideWith` в тестируемый тип:

```go
type llmStage struct {
    // поля — на усмотрение исполнителя; плюс два хука для тестов:
    logf        func(format string, args ...any) // nil → log.Printf
    afterShadow func()                           // nil в проде; зовётся после обработки report тени
}

// newLLMStage: provs выровнен с cfg.Providers (как возвращает llmProviders).
// Провайдеры с shadow:false — в llm.Judge{Policy: cfg.Policy}, с shadow:true —
// в llm.Shadow{Timeout: timeout, Max: llmShadowMaxInFlight}; Shadow nil, если
// теневых нет. Для каждого провайдера помнит его индекс в cfg.Providers —
// для ProviderPromptFor.
func newLLMStage(cfg config.LLM, provs []llm.Provider, reg *ops.Registry, timeout time.Duration) *llmStage

// adjudicate возвращает решение ГОЛОСУЮЩИХ (true = spam). Синхронно ждёт только
// голосующих (контекст WithTimeout(ctx, timeout)); тень запускает асинхронно.
func (s *llmStage) adjudicate(ctx context.Context, m domain.Message) bool
```

`const llmShadowMaxInFlight = 8`.

Поведение `adjudicate` (строго):

1. `out := judge.AdjudicateEach(cctx, llmMessageText(m), промпты голосующих по ProviderPromptFor(индекс, m.ChatID))`.
2. Существующие метрики — как на BASE и ТОЛЬКО по голосующим: при `out.Failed > 0` тот же
   `log.Printf("llm: %d/%d provider(s) failed: %v", …)` (через `logf`) и
   `tg_antispam_llm_provider_failures_total` += Failed; одна выборка `tg_antispam_llm_checks_total`
   с `result` = `error`, если `Failed >= Total`, иначе `boolLabel(out.Spam)`. Обозначим этот `result`
   как «итог голосующих».
3. По каждому голосу голосующих: `tg_antispam_llm_provider_checks_total` +1 с метками
   `provider=<Vote.Provider>`, `role=vote`, `result` = `error` (если `!OK`), иначе `spam`/`ham`.
4. Если тень есть: `Observe(ctx, тот же текст, промпты теневых по ProviderPromptFor, report)`.
   - `false` → по каждому теневому провайдеру `tg_antispam_llm_provider_checks_total`
     `{provider=<Name()>, role=shadow, result=dropped}` +1.
   - `report(votes)`: по каждому голосу — `tg_antispam_llm_provider_checks_total`
     `{provider, role=shadow, result=error|spam|ham}` +1; `tg_antispam_llm_shadow_agreement_total`
     `{provider, voter=<итог голосующих>, shadow=<error|spam|ham>}` +1; и строка лога ровно
     `llm shadow: chat=%d msg=%d provider=%s voter=%s shadow=%s score=%s`, где score — `%.3f` или
     `-`, если `Score == nil`. Текста сообщения и ошибок провайдера в строке нет. После обработки —
     `afterShadow()`, если задан.
5. Вернуть `out.Spam`. Ответ тени не влияет ни на возврат, ни на метрики из п.2.

`main.go`: вместо `llmJudge` собирается `llmStage` (при тех же условиях, что сейчас `llmJudge`);
`BayesAlwaysBorderline` и условие вызова стадии — `stage != nil`; в `decideWith` блок
`Adjudicate…метрики` заменяется на `if stage.adjudicate(workCtx, m) { v = <тот же llm-вердикт> ; ok = true }`.
Лог-строку о включении стадии дополнить числом теневых. `llmProviders` передаёт провайдеру OpenAI/
Anthropic/JEV в поле `Prompt` по-прежнему `cfg.Prompt` (промпт на вызов всё равно приходит аргументом).

### 3.4 Документация

- `config.example.yaml`: описать `shadow`, `prompt`, `prompt_overrides` у провайдера, порядок выбора
  промпта, правило покрытия исключений чатов и пример JEV в тени рядом с голосующим OpenAI.
- `README.md` (раздел про JEV/LLM) и `docs/architecture.md` (абзац про LLM-стадию ~312) — теневой
  режим, промпт на провайдера, новые метрики.
- `CHANGELOG.md`, `## [Unreleased]`: `### Added` — теневой провайдер, промпт на провайдера, метрики
  `tg_antispam_llm_provider_checks_total{provider,role,result}` и
  `tg_antispam_llm_shadow_agreement_total{provider,voter,shadow}`. Версию не бампать.

### 3.5 Тесты (только фейки и `httptest`, никакой реальной сети)

Новый `internal/llm/shadow_test.go`:

- `TestAdjudicateEachPerProviderPrompt` — два фейка запоминают prompt: каждый получил свой;
  `Adjudicate` с одним prompt отдаёт его обоим.
- `TestAdjudicateVotes` — `Votes` в порядке провайдеров, верные `Provider/Spam/OK/Err`; при истёкшем
  контексте неответивший имеет `OK=false` и `Err != nil`; `Spam/Failed/Total` совпадают с `Adjudicate`.
- `TestShadowReportsVotes` — `report` вызван ровно один раз с голосами в порядке провайдеров; у фейка-
  `Scorer` заполнен `Score`, `Classify` не вызывался, `Spam` истинен при `score == Cutoff()` и ложен
  при `score < Cutoff()`.
- `TestShadowDropsWhenFull` — `Max: 1`, провайдер блокируется: второй `Observe` → `false`, провайдер
  вызван один раз, `report` второго не вызван; после разблокировки и отчёта `Observe` снова `true`.
- `TestShadowTimeout` — провайдер дольше `Timeout` (например 50 мс): голос `OK=false`, `Err != nil`,
  `report` пришёл не позже `Timeout` + 1 с.
- `TestJEVScoreAndCutoff` — `httptest` отдаёт `noul: 0.42`: `Score` = 0.42, `Cutoff()` = `Threshold`,
  `Classify` true при `Threshold: 0.42` и false при `0.43`.

Новый `internal/config/llm_shadow_test.go`:

- `TestLLMProviderShadowPromptParse` — YAML с тремя новыми полями читается в структуру.
- `TestLLMRequiresVotingProvider` — все провайдеры `shadow: true` → ошибка со словом `voting`; один
  голосующий → без ошибки; при `llm.enabled: false` правило не действует.
- `TestLLMProviderPromptCoversChatOverrides` — у провайдера `prompt`, в `llm.prompt_overrides` чат X,
  у провайдера X нет → ошибка с `llm.providers[<i>]` и числом X; есть → без ошибки; провайдер без
  `prompt` → без ошибки.
- `TestProviderPromptForPrecedence` — все три уровня из §3.1 по очереди и пустая строка, когда пусто всё.

Новый `cmd/tg-antispam/llm_stage_test.go` (фейковые провайдеры, свой `ops.Registry`, `logf` пишет в
буфер, ожидание тени — через `afterShadow`):

- `TestLLMStageShadowNeverVotes` — голосующий ham + тень spam → `false`; голосующий spam + тень ham →
  `true`; тень с ошибкой → решение голосующего; `tg_antispam_llm_checks_total` и
  `tg_antispam_llm_provider_failures_total` отражают только голосующих.
- `TestLLMStageMetrics` — значения `tg_antispam_llm_provider_checks_total` с `role=vote|shadow` и
  `tg_antispam_llm_shadow_agreement_total` (включая `voter=error`, когда все голосующие упали), и
  `result=dropped`, когда слот тени занят.
- `TestLLMStageShadowLog` — строка лога точного формата §3.3 со `score=0.420` у Scorer и `score=-` у
  обычного провайдера; текста сообщения в логе нет.
- `TestLLMStagePerProviderPrompt` — голосующий и теневой получили каждый свой промпт по
  `ProviderPromptFor` для `m.ChatID`.

Существующие `*_test.go` — ни строки.

## 4. Не трогать

- Всё вне: `internal/llm/{llm.go,jev.go,shadow.go,shadow_test.go}`,
  `internal/config/{config.go,llm_shadow_test.go}`,
  `cmd/tg-antispam/{main.go,llm_providers.go,llm_stage.go,llm_stage_test.go}`, `config.example.yaml`,
  `README.md`, `docs/architecture.md`, `CHANGELOG.md` (AC-005). В частности `internal/detect`,
  `internal/watch`, `internal/telegram`, `internal/store`, `deploy/`, `.github/`, `go.mod`, `go.sum`,
  `internal/llm/{openai.go,anthropic.go}`.
- Семантика `Adjudicate`, `decide`, `classifyReply`, `promptOr`; метрики `tg_antispam_llm_checks_total`,
  `tg_antispam_llm_provider_failures_total`, `tg_antispam_incidents_total` — без изменений.
- Карточку инцидента и сигнал `llm` не менять. Релиз (бамп `Chart.yaml`, тег) — не делать. Живой
  Telegram, прод, k8s, токены, реальные запросы к JEV/OpenAI, host tmux, сигналы чужим процессам.

## 5. Разрешения

Docker — `./scripts/dev.sh` (golang:1.27.3 под `--user $(id -u):$(id -g)`) и `golang:1.27.3` для
`gofmt`. Новых модулей нет. Дифф `BASE..HEAD` ≤ 90000 байт (AC-007). Коммиты ≤50 символов, imperative,
без подписей и Co-Authored-By.

## 6. Критерии приёмки

- **AC-001.** Полный сьют с гонками зелёный:
  `bash -c './scripts/dev.sh test -race -count=1 ./...'`
- **AC-002.** vet, сборка и gofmt чистые:
  `bash -c './scripts/dev.sh vet ./... && ./scripts/dev.sh build ./... && test -z "$(docker run --rm -u "$(id -u):$(id -g)" -v "$PWD":/src -w /src golang:1.27.3 gofmt -l cmd internal)"'`
- **AC-003.** Новые тесты проходят (поимённо):
  `bash -c 'ts="TestAdjudicateEachPerProviderPrompt TestAdjudicateVotes TestShadowReportsVotes TestShadowDropsWhenFull TestShadowTimeout TestJEVScoreAndCutoff TestLLMProviderShadowPromptParse TestLLMRequiresVotingProvider TestLLMProviderPromptCoversChatOverrides TestProviderPromptForPrecedence TestLLMStageShadowNeverVotes TestLLMStageMetrics TestLLMStageShadowLog TestLLMStagePerProviderPrompt"; re=$(printf "%s|" $ts); out=$(./scripts/dev.sh test -race -count=1 -v -run "^(${re%|})$" ./internal/llm/ ./internal/config/ ./cmd/tg-antispam/ 2>&1) && for t in $ts; do printf "%s\n" "$out" | grep -Eq -- "^--- PASS: $t( |$)" || exit 1; done'`
- **AC-004.** Существующие тесты не изменены, не удалены и не переименованы:
  `bash -c 'test -z "$(git diff --name-only --diff-filter=MDR c784bb325103301cca92ebbd97bc15461249b0d7..HEAD -- "*_test.go")"'`
- **AC-005.** Состав работы в границах, дерево чистое:
  `bash -c 'f=$(mktemp) && git diff --name-only c784bb325103301cca92ebbd97bc15461249b0d7..HEAD > "$f" && test -s "$f" && ! grep -qvE "^(CHANGELOG\.md|README\.md|config\.example\.yaml|docs/architecture\.md|cmd/tg-antispam/(main|llm_providers|llm_stage|llm_stage_test)\.go|internal/llm/(llm|jev|shadow|shadow_test)\.go|internal/config/(config|llm_shadow_test)\.go)$" "$f" && test -z "$(git status --porcelain -- . ":(exclude)report.json" ":(exclude)report-blocked.md" ":(exclude)tmp" ":(exclude).gopath")"'`
- **AC-006.** Стадия подключена через llmStage, метрики и поля на месте:
  `bash -c 'git grep -q "newLLMStage(" -- cmd/tg-antispam/main.go && ! git grep -q "llmJudge" -- cmd/tg-antispam/main.go && git grep -q "tg_antispam_llm_provider_checks_total" -- cmd/tg-antispam/llm_stage.go && git grep -q "tg_antispam_llm_shadow_agreement_total" -- cmd/tg-antispam/llm_stage.go && git grep -q "yaml:\"shadow\"" -- internal/config/config.go && git grep -q "shadow:" -- config.example.yaml'`
- **AC-007.** Дифф влезает в потолок:
  `bash -c 'n=$(git diff c784bb325103301cca92ebbd97bc15461249b0d7..HEAD | wc -c) && test "$n" -gt 0 && test "$n" -le 90000'`
- **AC-008.** CHANGELOG — запись в Unreleased, деплой не тронут:
  `bash -c 'sed -n "/^## \[Unreleased\]/,/^## \[0/p" CHANGELOG.md | grep -q "^### Added" && sed -n "/^## \[Unreleased\]/,/^## \[0/p" CHANGELOG.md | grep -q "tg_antispam_llm_shadow_agreement_total" && git diff --quiet c784bb325103301cca92ebbd97bc15461249b0d7..HEAD -- deploy'`

## 7. Контракт отчёта

`report.json` в корне клона, untracked:

```json
{"criteria": [{"id": "AC-001", "status": "pass|fail|blocked",
               "command": "<команда из §6 посимвольно>", "rc": 0, "note": "…"}]}
```

Ровно восемь записей AC-001…AC-008, каждая перезапущена на финальном HEAD; у `blocked` `rc: null` и
дословная ошибка в `note`. Работа закоммичена (HEAD клона ≠ BASE).

## 8. Контракт на невыполнимое

Спека противоречит себе, факт §2 опровергнут, существующий тест падает от новой семантики, нужно
трогать пути §4 или реальную сеть — **стоп**: `report-blocked.md` с дословной командой, выводом и
пунктом спеки. Обходить несовместимость запрещено: `--no-deps`, `GOTOOLCHAIN=local`, `|| true`,
`set +e` в критерии, `sudo`, `git push`, ослабление/удаление чужих тестов, `replace` в `go.mod`.

## 9. Авторевью

В этой вехе авторевью исполнитель НЕ запускает: перекрёстное ревью диффа (agy и Grok) и мутации (Grok)
проводит координатор после приёмки, исправления по ним — отдельным заходом.

## 10. Стыки

Координатор после приёмки: ревью, мутации по новым тестам, релиз 0.22.0 (бамп `Chart.yaml`, тег),
`romtk3s`: `targetRevision`, `llmSecret.jevKey: JEV_TOKEN`, JEV `shadow: true` со своим `prompt` и
`prompt_overrides` для крипто-чата. Перевод JEV в голосующие — отдельное решение по данным тени; тогда
же решить вопрос атрибуции провайдера в карточке инцидента.
