# Веха: fake_admin по одному отображаемому имени — на ревью, детали совпадения в лог и карточку

- Репозиторий `/home/deploy/github/telegram-antispam`, дата 04.10.2026.
- BASE_SHA: `00a802a229ecefd8e0574269375558af0d9864a8` («Release version 0.18.0», `main`). Клон снят
  ровно с BASE. Спека в клоне не лежит: читай её по абсолютному пути
  `/home/deploy/github/telegram-antispam/docs/specs/fake-admin-name-review.md`.
- Исполнитель: `cx` (Codex). Исправления по ревью — тот же исполнитель, один заход. Мутации по новым
  тестам потом гоняет противоположный исполнитель (Grok) — не часть этой вехи.
- Критериев: 10.

## 0. Где работать

Клон `/home/deploy/exec-clones/telegram-antispam-fakeadmin-review-20261004`, ветка `fake-admin-name-review`.
Живое дерево `/home/deploy/github/telegram-antispam` не трогать. **push в origin запрещён** — работу
заберёт постановщик через `git fetch` из клона. `git add` только по именам, никогда `-A`. `.gopath/`
(кэш модулей) постановщик положил в клон до запуска. `tmp/` — для улик ревью, не коммитить. До первой
правки прочитай `AGENTS.md`, `internal/detect/fakeadmin.go`, `internal/detect/cascade.go`,
`internal/incident/machine.go` (функции `logOutcome`, `signalNames`), `internal/incident/card.go`.

## 1. Задача и почему

Инцидент 04.10.2026: в чате «Все кальянщики паттайи» (`-1002845427973`) детектор `fake_admin` дал
`delete_mute` участнику с отображаемым именем «Дмитрий» за обычный текст. Лог прода (pod
`tg-antispam-6cf7ff5598-gpzv4`): `chat=-1002845427973 msg=2261 sender=user: enforced incident=283
action=delete_mute outcome=succeeded action_ok=true deleted=true [fake_admin]`. С каким админом и по
какому полю совпало — из лога не узнать: детали сигнала туда не пишутся.

Решение владельца 04.10.2026:

1. Если совпало ТОЛЬКО отображаемое имя админа (`AdminIdentity.DisplayName`), а его username и custom
   title не совпали, — карточка в админ-чат БЕЗ санкции (тот же review-only путь, что у
   `captionless_media`/`bot_keyboard`).
2. Совпадение с username или custom title админа, а также подозрительный sender tag — санкция, как сейчас.
3. С каким админом и по какому полю совпало — в строку лога исхода инцидента и в карточку улики.

Что при этом ломается и чинится в этой же вехе: формат `Signal.Detail` у `fake_admin` меняется (сейчас
он содержит имена — их в лог писать нельзя, см. П6); `ReviewCandidate` сейчас вообще не запускается,
если выключены оба review-этапа (П4).

## 2. Что проверено постановщиком 04.10.2026, а что предположение

Проверено чтением кода BASE:

- **П1.** `detect.CheckFakeAdmin(m, admins, cfg) (domain.Signal, bool)` (`internal/detect/fakeadmin.go`):
  выключен → нет хита; для КАЖДОГО админа по порядку, для поля отправителя из `{username, display_name}`
  (в нижнем регистре) и поля админа из `{username, display_name, custom_title}` — `cfg.nameMatch`
  (пустые не совпадают; `MaxDistance<=0` — только равенство; короче `MinFuzzyLen` рун — только
  равенство; иначе `LevenshteinWithin(a, b, MaxDistance)`), ПЕРВОЕ совпадение возвращается сразу. Потом
  `SenderTag` против `SuspiciousTags` (точное равенство без регистра). `Detail` сейчас:
  `name '<отправитель>' matches admin '<имя админа>'` и `suspicious sender tag: <тег>` — оба содержат
  текст пользователя. `AdminIdentity{UserID int64; Username, DisplayName, CustomTitle string}`.
  `LevenshteinWithin` возвращает только bool, расстояния нет.
- **П2.** `Cascade.Decide` (`internal/detect/cascade.go`, стр. ~165): `if c.FakeAdmin.Enabled && !trusted`
  → `CheckFakeAdmin` → `c.actionable(sig)` (DefaultAction, Confidence 1.0). Порядок: иммунитет админов →
  blocklist → rules → fake_admin → behavior → Bayes. Первый хит выигрывает.
- **П3.** `Cascade.ReviewCandidate(m)` вызывается в `cmd/tg-antispam/main.go` (стр. ~913) в `decideWith`
  ПОСЛЕ `Decide` и LLM, только если `!ok && v.Reason != detect.ReasonAdminLookupUnavailable`. Внутри:
  `reviewSignal` (captionless_media, затем bot_keyboard) → доверенный отправитель → нет хита → при
  `c.Admins != nil` ошибка списка или текущий админ → нет хита → `c.review(sig)`. `review` даёт
  `Action: ActionQuarantine`, `ReviewOnly: true`, `Reason: sig.Name`, `Signals: []Signal{sig}`,
  Confidence 0.
- **П4.** Первая строка `ReviewCandidate`: `if c.CaptionMinLen <= 0 && !c.ReviewKeyboard { return … false }`.
- **П5.** Review-only безопасен ниже по конвейеру без правок: `internal/telegram/bot.go` (~397)
  `if verdict.ReviewOnly { dryRun = true }`; `incident.Machine.handle` (~260)
  `if inc.DryRun || inc.Verdict.ReviewOnly` → лог `not enforced … review_only=true`, состояние Done, без
  `Enforce`. Карточка: `outcomeLabel` → `review only: nothing applied (suggested quarantine)`, кнопка
  «Spam: enforce now» есть.
- **П6.** Лог исхода — `Machine.logOutcome` (`internal/incident/machine.go`): формат
  `chat=%d msg=%d sender=%s: %s incident=%d action=%s %s%s [%s]%s`, где `[%s]` — `signalNames`, только
  ИМЕНА сигналов. Комментарий у `signalNames` и тест `TestSanctionLogLeaksNothingUserControlled`
  (`internal/incident/log_test.go`) требуют: в лог не попадает ничего, что контролирует пользователь, в
  том числе `Detail` у `fake_admin` с именами. Тест подсовывает `Detail` `name '<canary>' matches admin
  '<canary>'` и проверяет отсутствие canary. Этот инвариант сохраняется (см. §3.2).
- **П7.** Карточка — `formatCard(id, inc, chatTitle, note, willAct)` (`internal/incident/card.go`): строки
  `#id reason → outcome`, необязательная note, `chat: …`, `from: …`; `Detail` сигналов не выводится;
  внешние поля через `sanitize` и `clip`.
- **П8.** Конфиг (`internal/config/config.go`): ключи `detection.fake_admin_enabled` (*bool, умолч. true),
  `fake_admin_max_distance` (умолч. 1), `fake_admin_min_fuzzy_len` (умолч. 5), `fake_admin_suspicious_tags`.
  Умолчания — `applyDetectionDefaults`, enum-проверки — `Validate` (образец: `detection.bayes_scope`,
  ошибка `detection.bayes_scope must be global|per_chat, got %q`). `Parse` вызывает `Validate`. Прод
  (`romtk3s/op1/tg-antispam/values.yaml`) ни одного `fake_admin_*` ключа не задаёт — работает на
  умолчаниях; задаёт `trust_threshold: 5`, `media_caption_min_len: 20`, `meaningful_min_len: 10`,
  `review_keyboard: true`.
- **П9.** `cmd/tg-antispam/main.go` (~852) собирает `detect.FakeAdminCfg{Enabled, SuspiciousTags,
  MaxDistance, MinFuzzyLen}` из конфига. `watch.MemberWatcher` (переименования) только шлёт уведомление,
  санкций не даёт — вне задачи.
- **П10.** Импорт: `incident` не импортирует `detect`; оба импортируют `domain` (без зависимостей).
- **П11.** Пакеты `detect`, `incident`, `config`, `domain` на BASE зелёные:
  `./scripts/dev.sh test -count=1 ./internal/detect/ ./internal/incident/ ./internal/config/ ./internal/domain/`.

Предположения (не проверено):

- **П12.** В инциденте 283 совпало именно отображаемое имя «Дмитрий» с отображаемым именем админа. Список
  админов чата постановщик не запрашивал (нет доступа к Telegram). Если совпало иное поле — новое
  поведение всё равно даст санкцию; это выяснит первая же строка лога после релиза.

## 3. Что сделать

### 3.1 `internal/domain` — формат детали (новый файл `internal/domain/fakeadmin.go`)

- Константы полей: `FakeAdminFieldUsername = "username"`, `FakeAdminFieldDisplayName = "display_name"`,
  `FakeAdminFieldCustomTitle = "custom_title"`.
- `func FakeAdminMatchDetail(senderField, adminField string, adminID int64, exact bool) string` →
  `<senderField>~<adminField> admin_id=<adminID> exact|fuzzy` (`exact`, если строки после приведения к
  нижнему регистру равны). Пример: `display_name~display_name admin_id=500 fuzzy`.
- `const FakeAdminTagDetail = "sender_tag~suspicious_tags"`.
- `func IsFakeAdminDetail(s string) bool` — true только для строк ровно этой грамматики:
  `^(username|display_name)~(username|display_name|custom_title) admin_id=-?[0-9]{1,20} (exact|fuzzy)$`
  или равенство `FakeAdminTagDetail`. В детали НЕТ имён, username и текста пользователя — только имена
  полей, id админа и признак точности.

### 3.2 `internal/detect/fakeadmin.go`

- `FakeAdminCfg` получает `NameMatchSanction bool`: false (нулевое значение) — совпадение только по
  отображаемому имени админа идёт на ревью; true — прежнее поведение (санкция).
- Тип `FakeAdminStrength` и константы `FakeAdminNoMatch`, `FakeAdminNameOnly`, `FakeAdminStrong`.
- `func ClassifyFakeAdmin(m domain.Message, admins []AdminIdentity, cfg FakeAdminCfg) (domain.Signal, FakeAdminStrength)`:
  1. выключен → `NoMatch`;
  2. перебрать ВСЕ пары (админы по порядку списка; поле отправителя: username, затем display_name; поле
     админа: username, custom_title, display_name), не выходя на первом совпадении. Первая пара с полем
     админа username или custom_title → `Strong` с деталью `FakeAdminMatchDetail(…, admin.UserID, …)`;
  3. сильной пары нет, но `SenderTag` подозрительный (как сейчас) → `Strong`, `Detail = FakeAdminTagDetail`;
  4. иначе первая пара с полем админа display_name → `NameOnly` (при `cfg.NameMatchSanction` → `Strong`),
     деталь той пары;
  5. иначе `NoMatch`.
  Имя сигнала всегда `fake_admin`.
- `CheckFakeAdmin` остаётся с прежней сигнатурой как обёртка: `sig, s := ClassifyFakeAdmin(…)`;
  `return sig, s != FakeAdminNoMatch` (любое совпадение — хит, как раньше; деталь — новая).

### 3.3 `internal/detect/cascade.go`

- `Decide`: стадия fake_admin вызывает `ClassifyFakeAdmin` и возвращает `c.actionable(sig)` ТОЛЬКО при
  `FakeAdminStrong`. `NameOnly` не возвращает ничего — каскад идёт дальше (behavior, Bayes), чтобы
  совпадение имени не перебивало более сильные детекторы и не отключало LLM.
- `ReviewCandidate`:
  - ранний выход только если `CaptionMinLen <= 0 && !ReviewKeyboard && !FakeAdmin.Enabled`;
  - собрать до двух сигналов: прежний `reviewSignal` (если хит) и fake_admin `NameOnly` (только при
    `c.Admins != nil`, списке без ошибки и `ClassifyFakeAdmin(...) == FakeAdminNameOnly`);
  - прежние гарды — доверенный отправитель, ошибка списка админов, текущий админ — отменяют ОБА;
  - вердикт как у `c.review`: `Signals` в порядке [review-сигнал, fake_admin], `Reason` — имя первого,
    `ReviewOnly: true`, `Action: ActionQuarantine`, Confidence 0. Если хит только по `reviewSignal` —
    результат ровно как на BASE.
- Комментарии у `ReviewCandidate`/`Decide`, говорящие «только captionless», обновить.

### 3.4 `internal/incident`

- `logOutcome`: после `[имена сигналов]` дописать ` fake_admin_match="<detail>"` для ПЕРВОГО сигнала с
  `Name == "fake_admin"` и `domain.IsFakeAdminDetail(Detail) == true`. Деталь, не прошедшая грамматику,
  в лог не пишется (тест с canary остаётся зелёным без правок). Хвост `: <ошибка>` — после этой вставки.
  Строка остаётся одной (деталь из грамматики переводов строк не содержит).
- `formatCard`: после строки `from: …` — строка `match: <detail>` для первого сигнала `fake_admin` с
  непустым `Detail`, через `sanitize` и `clip(…, maxCardNote)`. Нет такого сигнала — строки нет.

### 3.5 `internal/config`

- `Detection.FakeAdminNameMatch string` с yaml-тегом `fake_admin_name_match`; константы
  `FakeAdminNameMatchReview = "review"`, `FakeAdminNameMatchSanction = "sanction"`. Пусто →
  `review` в `applyDetectionDefaults`. `Validate`: иное значение → ошибка, содержащая
  `detection.fake_admin_name_match`.

### 3.6 `cmd/tg-antispam/main.go`

- В `detect.FakeAdminCfg` каскада: `NameMatchSanction: cfg.Detection.FakeAdminNameMatch == config.FakeAdminNameMatchSanction`.
  `MemberWatcher` не трогать.

### 3.7 Документация

- `config.example.yaml`: ключ `fake_admin_name_match: review` рядом с прочими `fake_admin_*`, комментарий:
  что идёт на ревью, что остаётся санкцией, `sanction` — прежнее поведение.
- `CHANGELOG.md`, `## [Unreleased]`, `### Changed`: смысл изменения, ключ `fake_admin_name_match`,
  `fake_admin_match=` в логе и `match:` в карточке.

### 3.8 Тесты (имена обязательны)

`internal/detect` (новый файл `internal/detect/fakeadmin_review_test.go`):

- `TestFakeAdminDisplayNameOnlyIsReview` — отправитель `{UserID 7, DisplayName "Дмитрий"}`, админ
  `{UserID 500, Username "boss_real", DisplayName "Дмитрии", CustomTitle "Владелец"}`, каскад с
  `CaptionMinLen 0`, `ReviewKeyboard false`, `FakeAdmin{Enabled, MaxDistance 1, MinFuzzyLen 5}`:
  `Decide` не actionable; `ReviewCandidate` → ok, `ReviewOnly`, `ActionQuarantine`, `Reason "fake_admin"`,
  один сигнал, `Detail == "display_name~display_name admin_id=500 fuzzy"`.
- `TestFakeAdminUsernameMatchSanctions` — username отправителя ~ username админа → `Decide` actionable
  с DefaultAction, не `ReviewOnly`, Detail `username~username admin_id=…`; плюс display name
  отправителя == username админа → тоже санкция, Detail `display_name~username …`.
- `TestFakeAdminCustomTitleMatchSanctions` — display name отправителя ~ custom title админа → санкция,
  Detail `display_name~custom_title …`.
- `TestFakeAdminStrongMatchWinsOverNameMatch` — админ A совпадает только по display name, админ B по
  username: санкция с `admin_id` B при обоих порядках списка.
- `TestFakeAdminSenderTagSanctions` — подозрительный тег без совпадения имён → санкция,
  Detail == `domain.FakeAdminTagDetail`.
- `TestFakeAdminNameMatchSanctionOption` — `NameMatchSanction: true`: случай первого теста даёт
  санкцию в `Decide`, а `ReviewCandidate` fake_admin не возвращает.
- `TestFakeAdminNameReviewGuards` — по подтестам: доверенный отправитель, текущий админ (тот же UserID),
  ошибка списка админов, `FakeAdmin.Enabled=false`, `Admins == nil` → `ReviewCandidate` не хит.
- `TestFakeAdminNameMatchDoesNotPreemptLaterStages` — совпадение только по display name + Bayes,
  настроенный на спам (образец — тест Bayes в `cascade_test.go`): `Decide` → `Reason "bayes"`.
- `TestFakeAdminNameReviewWithCaptionlessMedia` — фото без подписи + совпадение имени, `CaptionMinLen 20`:
  два сигнала `[captionless_media, fake_admin]`, `Reason "captionless_media"`.

`internal/domain`: `TestFakeAdminDetailGrammar` — выход `FakeAdminMatchDetail` для всех 6 сочетаний
полей и отрицательного id проходит `IsFakeAdminDetail`; не проходят: `name 'x' matches admin 'x'`,
строка с `\n`, кириллица, лишний пробел, пустая.

`internal/incident` (новый файл `internal/incident/fakeadmin_log_test.go`):

- `TestFakeAdminDetailInEnforceLog` — санкция с сигналом fake_admin и деталью
  `username~username admin_id=42 fuzzy`: строка `enforced` содержит
  `[fake_admin] fake_admin_match="username~username admin_id=42 fuzzy"`, строк ровно одна.
- `TestFakeAdminDetailInReviewLog` — ReviewOnly-инцидент с деталью display_name: строка `not enforced`
  содержит `review_only=true` и `fake_admin_match="display_name~display_name admin_id=500 fuzzy"`;
  санкции в вызовах fake-порта нет.
- `TestUnsafeFakeAdminDetailNotLogged` — деталь `name 'Дмитрий' matches admin 'Дмитрий'`: в логе нет
  `Дмитрий` и нет `fake_admin_match=`.
- `TestCardShowsFakeAdminMatch` — `formatCard` с сигналом fake_admin содержит
  `match: display_name~display_name admin_id=500 fuzzy`; карточка без fake_admin строки `match:` не имеет.

`internal/config` (новый файл `internal/config/fakeadmin_test.go`):

- `TestFakeAdminNameMatchDefaultsAndValidates` — без ключа → `review`; `sanction` принимается;
  `ban` → ошибка с `detection.fake_admin_name_match`.
- `TestFakeAdminNameMatchBackwardCompatible` — `UnknownKeys` принимает новый ключ; конфиг с прежними
  `fake_admin_enabled: true`, `fake_admin_max_distance: 1`, `fake_admin_min_fuzzy_len: 5` и блоком
  detection прода из П8 парсится, умолчания прежние плюс `review`; `config.example.yaml` парсится и
  даёт `review`.

## 4. Не трогать

- `internal/watch`, `internal/telegram`, `internal/admin`, `internal/store`, `internal/queue`,
  `internal/blocklist`, `internal/llm`, `internal/ops`, `internal/selfcheck`, `internal/train`.
- Существующие тесты: не удалять, не ослаблять, строки не менять — только добавлять новые файлы/функции
  (AC-006 проверяет отсутствие удалённых строк в шести файлах). Если существующий тест падает от новой
  семантики — стоп по §8, не правь.
- Формат строки лога до `[имена]` и сама `[имена]`; `signalNames` выводит только имена, как сейчас.
- `.github/`, `Dockerfile`, `Makefile`, `scripts/`, `go.mod`, `go.sum`, `deploy/`, `docs/specs/`,
  `docs/spec-queue.md`, `README.md`. Релиз (бамп `appVersion`, тег) — не делать.
- Живой Telegram, прод, k8s, токены. Host tmux, сигналы чужим процессам.

## 5. Разрешения

Сеть — только модульный прокси через `./scripts/dev.sh` (новых модулей нет). Docker — `./scripts/dev.sh`
(golang:1.26.6, `--user $(id -u):$(id -g)` = 1002:1002) и `golang:1.26.6` для `gofmt`. Новые файлы —
только в путях AC-009. Дифф `BASE..HEAD` ≤ 60000 байт (AC-010).

## 6. Критерии приёмки

- **AC-001.** Полный сьют с гонками зелёный:
  `bash -c './scripts/dev.sh test -race -count=1 ./...'`
- **AC-002.** vet, сборка и gofmt чистые:
  `bash -c './scripts/dev.sh vet ./... && ./scripts/dev.sh build ./... && test -z "$(docker run --rm -u "$(id -u):$(id -g)" -v "$PWD":/src -w /src golang:1.26.6 gofmt -l cmd internal)"'`
- **AC-003.** Классификация fake_admin и путь ревью:
  `bash -c 'out=$(./scripts/dev.sh test -count=1 -v -run "^TestFakeAdmin(DisplayNameOnlyIsReview|UsernameMatchSanctions|CustomTitleMatchSanctions|StrongMatchWinsOverNameMatch|SenderTagSanctions|NameMatchSanctionOption|NameReviewGuards|NameMatchDoesNotPreemptLaterStages|NameReviewWithCaptionlessMedia)$" ./internal/detect/ 2>&1) && for t in DisplayNameOnlyIsReview UsernameMatchSanctions CustomTitleMatchSanctions StrongMatchWinsOverNameMatch SenderTagSanctions NameMatchSanctionOption NameReviewGuards NameMatchDoesNotPreemptLaterStages NameReviewWithCaptionlessMedia; do printf "%s\n" "$out" | grep -Eq -- "^--- PASS: TestFakeAdmin$t( |$)" || exit 1; done'`
- **AC-004.** Грамматика детали, лог и карточка:
  `bash -c 'a=$(./scripts/dev.sh test -count=1 -v -run "^TestFakeAdminDetailGrammar$" ./internal/domain/ 2>&1) && b=$(./scripts/dev.sh test -count=1 -v -run "^(TestFakeAdminDetailInEnforceLog|TestFakeAdminDetailInReviewLog|TestUnsafeFakeAdminDetailNotLogged|TestCardShowsFakeAdminMatch)$" ./internal/incident/ 2>&1) && printf "%s\n" "$a" | grep -Eq -- "^--- PASS: TestFakeAdminDetailGrammar( |$)" && for t in TestFakeAdminDetailInEnforceLog TestFakeAdminDetailInReviewLog TestUnsafeFakeAdminDetailNotLogged TestCardShowsFakeAdminMatch; do printf "%s\n" "$b" | grep -Eq -- "^--- PASS: $t( |$)" || exit 1; done'`
- **AC-005.** Конфиг и обратная совместимость:
  `bash -c 'out=$(./scripts/dev.sh test -count=1 -v -run "^(TestFakeAdminNameMatchDefaultsAndValidates|TestFakeAdminNameMatchBackwardCompatible)$" ./internal/config/ 2>&1) && for t in TestFakeAdminNameMatchDefaultsAndValidates TestFakeAdminNameMatchBackwardCompatible; do printf "%s\n" "$out" | grep -Eq -- "^--- PASS: $t( |$)" || exit 1; done'`
- **AC-006.** Существующие тесты не изменены (в шести файлах нет удалённых строк):
  `bash -c 'test -z "$(git diff 00a802a229ecefd8e0574269375558af0d9864a8..HEAD -- internal/detect/fakeadmin_test.go internal/detect/review_test.go internal/detect/cascade_test.go internal/incident/log_test.go internal/incident/card_test.go internal/config/config_test.go | grep -E "^-" | grep -vE "^--- ")"'`
- **AC-007.** Опция проброшена в каскад, наблюдатель переименований не тронут:
  `bash -c 'grep -Eq "NameMatchSanction: +cfg\.Detection\.FakeAdminNameMatch == config\.FakeAdminNameMatchSanction" cmd/tg-antispam/main.go && git diff --quiet 00a802a229ecefd8e0574269375558af0d9864a8..HEAD -- internal/watch'`
- **AC-008.** Документация:
  `bash -c 'grep -Eq "^  fake_admin_name_match: review" config.example.yaml && sed -n "/^## \[Unreleased\]/,/^## \[0/p" CHANGELOG.md | grep -q "fake_admin_name_match"'`
- **AC-009.** Состав работы в границах, дерево чистое:
  `bash -c 'f=$(mktemp) && git diff --name-only 00a802a229ecefd8e0574269375558af0d9864a8..HEAD > "$f" && ! grep -qvE "^(CHANGELOG\.md|config\.example\.yaml|cmd/tg-antispam/main\.go|internal/(domain|detect|incident|config)/[a-z0-9_]+\.go)$" "$f" && test -z "$(git status --porcelain -- . ":(exclude)report.json" ":(exclude)report-blocked.md" ":(exclude)tmp")"'`
- **AC-010.** Ревью-дифф влезает в потолок:
  `bash -c 'n=$(git diff 00a802a229ecefd8e0574269375558af0d9864a8..HEAD | wc -c) && test "$n" -gt 0 && test "$n" -le 60000'`

## 7. Контракт отчёта

`report.json` в корне клона, схемы v2, untracked:

```json
{"schema_version": 2,
 "policy_id": "cross-review-v1",
 "handoff_status": "ready",
 "executor": {"backend": "codex", "model": "<точная модель>"},
 "spec_sha256": "<sha256 файла /home/deploy/github/telegram-antispam/docs/specs/fake-admin-name-review.md>",
 "base_sha": "00a802a229ecefd8e0574269375558af0d9864a8",
 "reviewed_sha": "<коммит, ушедший ревьюерам>",
 "final_sha": "<HEAD клона>",
 "review": {"resolutions": [], "initial_receipts": [], "verification_receipts": []},
 "criteria": [{"id": "AC-001", "status": "pass|fail|blocked",
               "command": "<команда-доказательство>", "rc": 0, "note": "…"}]}
```

Ровно десять записей AC-001…AC-010, `command` посимвольно из §6, каждая перезапущена на `final_sha`;
`handoff_status` — `ready|blocked|needs_owner`; у `blocked` `rc: null` и дословная ошибка в `note`;
цепочка `base_sha → reviewed_sha → final_sha` от предка к потомку.

## 8. Контракт на невыполнимое

Спека противоречит себе, факт §2 опровергнут, существующий тест падает от новой семантики, нужно трогать
пути §4 или живой Telegram — **стоп**: `report-blocked.md` с дословной командой, выводом и пунктом спеки.
Обходить несовместимость запрещено: `--no-deps`, `GOTOOLCHAIN=local`, `|| true`, `set +e` в критерии,
`sudo`, `git push`, ослабление/удаление чужих тестов, `replace` в `go.mod`. Дифф не влезает в 60000 байт
без потери обязательного — стоп, доложи `git diff --stat`.

## 9. Авторевью — обязательная часть вехи

Отчёт объявляет `policy_id: cross-review-v1`. Исполнитель Codex → ревьюеры `agy` и `grok`, параллельно,
на одном диапазоне, не показывая одному находки другого. Только чтение.

1. Реализуй, прогони критерии, закоммить. `REVIEW_SHA` = `HEAD` после последнего рабочего коммита.
2. Ревью (второй — та же команда с `grok`):

   ```
   bash /home/deploy/.claude/skills/executor-milestone/scripts/review_run.sh initial agy \
     --clone /home/deploy/exec-clones/telegram-antispam-fakeadmin-review-20261004 \
     --base 00a802a229ecefd8e0574269375558af0d9864a8 \
     --range 00a802a229ecefd8e0574269375558af0d9864a8..<REVIEW_SHA> \
     --context "tg-antispam: fake_admin по одному display name админа — review-only карточка без санкции; username/custom title/тег — санкция; деталь совпадения (поля, admin_id, без имён) в лог исхода и карточку; ключ detection.fake_admin_name_match; спека /home/deploy/github/telegram-antispam/docs/specs/fake-admin-name-review.md; только чтение"
   ```

3. `rc=0` ревью не доказывает: нужен вердикт (`ВЕРДИКТ:` `ПРИНЯТО`/`НЕ ПРИНИМАТЬ`). Временный сбой —
   один повтор; квота — `blocked`.
4. Каждую находку перепроверь по коду; подтверждённые исправь, критерии заново, закоммить. Заход ОДИН.
5. **`verify` — ТОЛЬКО если были правки** (`final_sha != reviewed_sha`): те же команды с `verify` и
   диапазоном `<REVIEW_SHA>..<FINAL_SHA>`. Правок не было — `verify` не запускать,
   `verification_receipts` пуст. После старта `verify` ничего не коммитить.
6. `review.resolutions` — по записи на находку (`finding_id`, `decision` из `fixed|disproved|needs_owner`,
   `source_receipt`, `proof`, `fix_commits` при `fixed`).

## 10. Стыки

Координатор после приёмки: мутации по новым тестам (Grok); стенд с синтетическим Bot API (отправитель с
display name админа → карточка `review only`, мута нет, в логе `fake_admin_match=`; username админа →
`delete_mute`); релиз отдельным шагом (бамп `appVersion`, тег). В values прода ключ не нужен —
умолчание `review` и есть решение владельца.
