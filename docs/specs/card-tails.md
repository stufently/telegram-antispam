# Веха: хвосты карточек 0.19.0 — claim до новой карточки, откат незарегистрированной карточки, «устарело» при занятом claim

- Репозиторий `/home/deploy/github/telegram-antispam`, дата 05.10.2026.
- BASE: `cc72b93054c2fe7793cf0230f22b96b84247b84d` («Release version 0.19.0», `main`). Клон снят ровно с BASE.
  Спека в клоне не лежит: читай её по абсолютному пути
  `/home/deploy/github/telegram-antispam/docs/specs/card-tails.md`; в клон её не копируй и не коммить
  (`git add` спеки не делай) — критерий чистоты дерева от этого не зависит.
- Исполнитель: `cx` (Codex). Ревью и мутации проводит постановщик после приёмки — НЕ часть вехи:
  ревьюеров сам не запускай.
- Критериев: 7.

## 0. Где работать

Клон `/home/deploy/exec-clones/antispam-card-tails-20261005`, ветка `card-tails`. Живое дерево
`/home/deploy/github/telegram-antispam` не трогать. **push в origin запрещён** — работу заберёт
постановщик через `git fetch` из клона (push-url и так `no-push`). `git add` только по именам, никогда
`-A`. `.gopath/` (кэш модулей) постановщик положил в клон до запуска. `tmp/` — для своих логов, не
коммитить. До первой правки прочитай `AGENTS.md`, `internal/incident/machine.go` (функции `process`,
`override`, `sendCard`), `internal/admin/callbacks.go` (`dispatch`, `releaseClaim`),
`internal/store/cards.go`, `internal/store/incidents.go` (`ClaimManualOverride`, `FinishManualOverride`),
`internal/incident/card_revision_test.go` и `internal/incident/fakeadmin_edit_test.go` (готовые стенды
`newEditRig`, `requireEditPromotion`, `cardPort`).

## 1. Задача и почему

В 0.19.0 (`ebb07cd`) у инцидента появилась «текущая карточка» (`incident_cards`): кнопки карточки, не
совпадающей с записанной, отвечают «устарело, см. новую карточку» и снимают свою клавиатуру. Ревью Codex
этого коммита нашло три дыры (вердикт «НЕ ПРИНИМАТЬ», выкачено по решению владельца, остаток — эта веха):

1. `internal/incident/machine.go` (`override`, ручная ветка): `FinishManualOverride` отпускает claim ДО
   `sendCard` новой карточки. В этом окне кнопка старой карточки берёт claim, `IsIncidentCard` для неё
   ещё true — и она снимает только что наложенную ручную санкцию или обучает Bayes на устаревшем решении.
2. `machine.go` (`sendCard`): если `SendAdmin` прошёл, а `SaveIncidentCard` упал, опубликованная карточка
   остаётся без регистрации и без отката. При автоматическом продвижении новые копии улики не попадают в
   `evidence`, новая карточка отвергается как устаревшая (запись указывает на старую), а следующая
   попытка публикует дубликат.
3. `internal/admin/callbacks.go` (`dispatch`): при занятом claim (решение уже принято на НОВОЙ карточке,
   либо идёт override) кнопка СТАРОЙ карточки отвечает `already decided: …` до проверки актуальности, и
   разметка старой карточки не снимается никогда.

## 2. Что проверено постановщиком 05.10.2026, а что предположение

Проверено чтением кода BASE:

- **П1.** `Machine.override` (machine.go ~361–437): `ClaimManualOverride` → для автоматического
  продвижения `GetIncident` и проверка `row.Action == quarantine` → `process` (сам шлёт карточку через
  `sendCard` до `Enforce`) либо, для ручного вердикта, `Enforce` → `FinishManualOverride(id, verdict,
  out.Sanctioned)` → `if automatic return out.Err` → `setState(Done)` при успешной санкции → `sendCard`
  ручной карточки (ошибка только логируется) → эфемерное уведомление.
- **П2.** `sendCard` (machine.go ~440): `SendAdmin` → при ошибке вернуть её; иначе
  `SaveIncidentCard(id, {adminChatID, mid})` и вернуть её ошибку. Вызывается из трёх мест: ветка
  `copyErr != nil` в `process`, успешная ветка `process` (после неё `AddEvidence(id, adminChatID,
  adminIDs)`), ручная ветка `override`. В `process` любая ошибка `sendCard` → `logOutcome(… "not
  enforced", "stage=admin_notify" …)` и возврат ДО `Enforce`.
- **П3.** `store.IsIncidentCard(id, card)` — true, если записи нет ИЛИ запись равна `card` (легаси-карточки
  принимаются до первой регистрации). `SaveIncidentCard` — upsert.
- **П4.** `admin.Handler.dispatch` (callbacks.go ~214–256): для всех действий, кроме `ActDeleteEvidence`,
  `RecordDecision(inc.ID, act)`; `!claimed` → сразу `"already decided: " + decisionLabel(existing)`.
  Проверка `IsIncidentCard` идёт ПОСЛЕ claim; несовпадение → `releaseClaim`, `EditAdminMarkup(cb.AdminChatID,
  cb.MessageID, nil)`, ответ `"устарело, см. новую карточку"`.
- **П5.** `ManualOverrideClaim == "enf"` — тот же decision, что у кнопки «enforce»; `FinishManualOverride`
  в конце снимает decision `enf` в той же транзакции.
- **П6.** Fake-порт (`internal/telegram/fake`): `SendAdmin` возвращает `SendAdminID`/`SendAdminErr`,
  `DeleteMessages` пишет `LastDelete` и возвращает `DeleteErr`, `EditAdminMarkup` только логирует вызов
  (аргументы не пишет — для проверки аргументов оберни порт в тесте, как `cardPort`). `Machine` берёт
  `Repo`-интерфейс — сбой `SaveIncidentCard` в тесте делается обёрткой над `*store.DB`.
- **П7.** Существующий `TestFakeAdminReviewSpamEditEnforces` требует при успешном продвижении ровно
  вызовы `CopyMessages, ChatTitle, SendAdmin, RestrictMember, DeleteMessages` — успешный путь новых
  вызовов получать не должен.
- **П8.** На BASE зелёные: `./scripts/dev.sh test -count=1 ./internal/incident/ ./internal/admin/ ./internal/store/`.

Предположений нет.

## 3. Что сделать

### 3.1 Находка 1 — claim держится до регистрации ручной карточки (`machine.go`, `override`)

Для ручного вердикта порядок становится: `Enforce` → (при `out.Sanctioned && out.Err == nil`)
`setState(Done)` → сборка и `sendCard` ручной карточки (как сейчас, ошибка логируется) →
`FinishManualOverride` → возврат `out.Err` / эфемерное уведомление (как сейчас). Автоматическая ветка
не меняется (её карточку `process` уже шлёт под claim). Итог: пока claim `enf` не снят, новая карточка
уже зарегистрирована, и ни одна кнопка (старой или новой карточки) не действует в окне. Поправь
комментарии, описывающие прежний порядок.

### 3.2 Находка 2 — незарегистрированная карточка откатывается (`machine.go`, `sendCard` и `process`)

- Пакетная ошибка `errCardNotRecorded` (неэкспортируемая). Если `SendAdmin` вернул `mid` без ошибки, а
  `SaveIncidentCard` упал: best-effort `DeleteMessages(ctx, adminChatID, []int{mid})`; если удаление
  упало — best-effort `EditAdminMarkup(ctx, adminChatID, mid, nil)`; сбои отката — в `log.Printf` с id
  инцидента. Вернуть ошибку, для которой `errors.Is(err, errCardNotRecorded)` истинно и текст которой
  содержит исходную ошибку записи.
- Вызывающие трактуют такую ошибку как «карточка не отправлена» — их текущие ветки ошибки уже это
  делают (стоп до `Enforce`, `stage=admin_notify`; в ручной ветке `override` — лог).
- Успешная ветка `process`: при `errors.Is(err, errCardNotRecorded)` дополнительно best-effort удалить
  только что скопированные `adminIDs` из админ-чата (их учёт так и не записан; повторная попытка
  скопирует улику заново). При обычном сбое `SendAdmin` поведение прежнее (копии не трогать).
- Успешный путь — без новых вызовов порта (П7).

### 3.3 Находка 3 — старая карточка при занятом claim (`internal/admin/callbacks.go`, `dispatch`)

При `!claimed`: сначала `IsIncidentCard(inc.ID, {cb.AdminChatID, cb.MessageID})`. Ошибка — вернуть её.
Карточка не текущая — `EditAdminMarkup(ctx, cb.AdminChatID, cb.MessageID, nil)` (ошибку игнорировать,
как в существующей ветке) и ответ `"устарело, см. новую карточку"`. Карточка текущая (или записи нет —
легаси) — прежний ответ `"already decided: " + decisionLabel(existing)`. Текст «устарело» и снятие
разметки вынеси в один хелпер, которым пользуются обе ветки (занятый и свободный claim). Claim в этой
ветке не наш — ничего не отпускать.

### 3.4 Документация

`CHANGELOG.md`, `## [Unreleased]`, `### Fixed`: три пункта по смыслу §1 (ручной override держит claim до
регистрации новой карточки; незарегистрированная карточка и её новые копии удаляются, повтор не
дублирует; старая карточка при уже принятом решении отвечает «устарело» и теряет кнопки). Версию не
бампать.

### 3.5 Тесты (новый файл `internal/incident/card_tails_test.go`, имена обязательны)

Каждый тест, кроме `TestDecidedLegacyCardStillAlreadyDecided` (он страхует уже существующее легаси-поведение и на BASE проходит), обязан ПАДАТЬ на BASE-реализации (проверь руками, лог — в `tmp/red.log`, в отчёт — note
AC-003) и проходить после правки. Стенды бери из существующих тестов, сами существующие файлы не меняй.

- `TestManualOverrideHoldsClaimUntilNewCard` — review-only/dry-run инцидент с зарегистрированной
  карточкой (mid A), затем ручной `/spam` (`manualIncident`-образный вердикт) с `SendAdminID = B`. В
  момент `SendAdmin` ручной карточки (обёртка порта) нажать `fp` на карточке A через
  `admin.Handler` — не должно быть `UnrestrictMember` и обучения. После завершения override: `fp` на A →
  ответ содержит «устарело», есть `EditAdminMarkup`, нет `UnrestrictMember`, тренер не вызван; `fp` на B →
  ровно один `UnrestrictMember`, тренер вызван один раз.
- `TestUnrecordedCardIsRolledBack` — продвижение review-инцидента спам-правкой (`newEditRig`), обёртка
  репо роняет `SaveIncidentCard` один раз на продвижении. Ожидание: `HandleReport` вернул ошибку,
  `RestrictMember` не вызван; удалены (в админ-чате 999) и id новой карточки, и id новых копий улики;
  записанная карточка и `ListEvidence` — прежние (старая карточка/улика); decision инцидента пуст. Затем
  повторное продвижение (запись работает) санкционирует ровно один раз, регистрирует ровно одну новую
  карточку, кнопки старой отвечают «устарело».
- `TestUnrecordedCardLosesButtonsWhenDeleteFails` — то же падение записи при `DeleteErr != nil`:
  `EditAdminMarkup` вызван для `mid` новой карточки в чате 999 с пустой разметкой.
- `TestDecidedIncidentOldCardIsStale` — продвижение (новая карточка B), решение `confirm` на B (принято).
  Затем `fp` и `lift` на старой карточке → ответ «устарело», `EditAdminMarkup` для старой карточки, нет
  `UnrestrictMember`, тренер не вызван повторно. `fp` на B → по-прежнему `already decided: confirmed spam`
  и без `EditAdminMarkup`.
- `TestDecidedLegacyCardStillAlreadyDecided` — инцидент без записи в `incident_cards` (легаси), decision
  уже принят: нажатие → `already decided: …`, `EditAdminMarkup` не вызван.

## 4. Не трогать

- Всё вне `internal/incident/machine.go`, `internal/admin/callbacks.go`, нового
  `internal/incident/card_tails_test.go` и `CHANGELOG.md` (AC-005). В частности `internal/store`
  (схема, запросы), `internal/telegram` (в том числе fake), `cmd/`, `deploy/`, `.github/`, `go.mod`,
  `go.sum`, `docs/`, `config.example.yaml`, `README.md`.
- Существующие тесты: не удалять, не ослаблять, не менять ни строки (AC-004). Падает существующий тест
  от новой семантики — стоп по §8.
- Релиз (бамп `Chart.yaml`, тег) — не делать. Живой Telegram, прод, k8s, токены, host tmux, сигналы
  чужим процессам.

## 5. Разрешения

Сеть — только модульный прокси через `./scripts/dev.sh` (новых модулей нет). Docker — `./scripts/dev.sh`
(golang:1.26.6 под `--user $(id -u):$(id -g)` = 1002:1002) и `golang:1.26.6` для `gofmt`. Дифф
`BASE..HEAD` ≤ 40000 байт (AC-007). Коммиты: ≤50 символов, без подписей и Co-Authored-By.

## 6. Критерии приёмки

- **AC-001.** Полный сьют с гонками зелёный:
  `bash -c './scripts/dev.sh test -race -count=1 ./...'`
- **AC-002.** vet, сборка и gofmt чистые:
  `bash -c './scripts/dev.sh vet ./... && ./scripts/dev.sh build ./... && test -z "$(docker run --rm -u "$(id -u):$(id -g)" -v "$PWD":/src -w /src golang:1.26.6 gofmt -l cmd internal)"'`
- **AC-003.** Новые тесты проходят (поимённо):
  `bash -c 'out=$(./scripts/dev.sh test -race -count=1 -v -run "^(TestManualOverrideHoldsClaimUntilNewCard|TestUnrecordedCardIsRolledBack|TestUnrecordedCardLosesButtonsWhenDeleteFails|TestDecidedIncidentOldCardIsStale|TestDecidedLegacyCardStillAlreadyDecided)$" ./internal/incident/ 2>&1) && for t in TestManualOverrideHoldsClaimUntilNewCard TestUnrecordedCardIsRolledBack TestUnrecordedCardLosesButtonsWhenDeleteFails TestDecidedIncidentOldCardIsStale TestDecidedLegacyCardStillAlreadyDecided; do printf "%s\n" "$out" | grep -Eq -- "^--- PASS: $t( |$)" || exit 1; done'`
- **AC-004.** Существующие тесты не изменены, удалены или переименованы:
  `bash -c 'test -z "$(git diff --name-only --diff-filter=MDR cc72b93054c2fe7793cf0230f22b96b84247b84d..HEAD -- "*_test.go")"'`
- **AC-005.** Состав работы в границах, дерево чистое:
  `bash -c 'f=$(mktemp) && git diff --name-only cc72b93054c2fe7793cf0230f22b96b84247b84d..HEAD > "$f" && ! grep -qvE "^(CHANGELOG\.md|internal/incident/machine\.go|internal/incident/card_tails_test\.go|internal/admin/callbacks\.go)$" "$f" && test -z "$(git status --porcelain -- . ":(exclude)report.json" ":(exclude)report-blocked.md" ":(exclude)tmp" ":(exclude).gopath")"'`
- **AC-006.** CHANGELOG — запись в Unreleased, версия не тронута:
  `bash -c 'sed -n "/^## \[Unreleased\]/,/^## \[0/p" CHANGELOG.md | grep -q "^### Fixed" && git diff --quiet cc72b93054c2fe7793cf0230f22b96b84247b84d..HEAD -- deploy'`
- **AC-007.** Дифф влезает в потолок:
  `bash -c 'n=$(git diff cc72b93054c2fe7793cf0230f22b96b84247b84d..HEAD | wc -c) && test "$n" -gt 0 && test "$n" -le 40000'`

## 7. Контракт отчёта

`report.json` в корне клона, untracked:

```json
{"criteria": [{"id": "AC-001", "status": "pass|fail|blocked",
               "command": "<команда из §6 посимвольно>", "rc": 0, "note": "…"}]}
```

Ровно семь записей AC-001…AC-007, каждая перезапущена на финальном HEAD; у `blocked` `rc: null` и
дословная ошибка в `note`. В `note` AC-003 — что каждый из четырёх тестов на находки падал на BASE-коде, а легаси-тест на BASE проходил (ссылка на
`tmp/red.log`). Работа закоммичена (HEAD клона ≠ BASE).

## 8. Контракт на невыполнимое

Спека противоречит себе, факт §2 опровергнут, существующий тест падает от новой семантики, нужно трогать
пути §4 или живой Telegram — **стоп**: `report-blocked.md` с дословной командой, выводом и пунктом спеки.
Обходить несовместимость запрещено: `--no-deps`, `GOTOOLCHAIN=local`, `|| true`, `set +e` в критерии,
`sudo`, `git push`, ослабление/удаление чужих тестов, `replace` в `go.mod`.

## 9. Авторевью

В этой вехе авторевью исполнитель НЕ запускает: перекрёстное ревью диффа (Codex и agy) и мутации проводит
координатор после приёмки, исправления по ним — отдельным заходом.

## 10. Стыки

Координатор после приёмки: Codex- и agy-ревью диффа, мутации по новым тестам (Grok), релиз отдельным
шагом (бамп `Chart.yaml`, тег, `romtk3s`).
