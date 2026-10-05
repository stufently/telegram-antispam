# Мутационная проверка новых тестов вехи card-tails

- Репозиторий: клон `/home/deploy/exec-clones/antispam-card-tails-mut-20261005`, ветка `card-tails`,
  BASE `9e575036213304e4cbc28105501d50b39c4b4c26`. Спека лежит вне клона, в клон её не копируй и не коммить.
- Исполнитель: `gk` (Grok). Код не исправляешь, коммитов не делаешь, push запрещён (push-url `no-push`).
- Критериев: 2.

## 1. Задача

Проверить, ловят ли пять новых тестов из `internal/incident/card_tails_test.go` поломки боевого кода.
Для каждого мутанта ниже по одному: внести правку в боевой файл → прогнать целевую команду → записать
исход (killed = команда упала из-за теста; survived = зелёная; invalid = не компилируется) → вернуть файл
`git checkout -- <файл>` и убедиться в `git diff --quiet`. Перед первым мутантом прогони целевую команду на
чистом дереве: она обязана быть зелёной.

Целевая команда (из корня клона):
`./scripts/dev.sh test -count=1 -run "^(TestManualOverrideHoldsClaimUntilNewCard|TestUnrecordedCardIsRolledBack|TestUnrecordedCardLosesButtonsWhenDeleteFails|TestDecidedIncidentOldCardIsStale|TestDecidedLegacyCardStillAlreadyDecided)$" ./internal/incident/`

Мутанты (`internal/incident/machine.go` = MG, `internal/admin/callbacks.go` = CB):

- M01 MG `override`: вызов `FinishManualOverride` перенести ПЕРЕД сборкой и `sendCard` ручной карточки.
- M02 MG `sendCard`: убрать `DeleteMessages` незарегистрированной карточки (сразу к `EditAdminMarkup`-фолбэку не
  переходить, ошибку по-прежнему вернуть).
- M03 MG `sendCard`: убрать фолбэк `EditAdminMarkup` после неудачного удаления.
- M04 MG `sendCard`: при сбое `SaveIncidentCard` вернуть `nil`.
- M05 MG `sendCard`: вернуть исходную ошибку записи без `errCardNotRecorded`.
- M06 MG `process`: убрать удаление `adminIDs` при `errCardNotRecorded`.
- M07 MG `process`: удалять `adminIDs` при ЛЮБОЙ ошибке `sendCard` (условие `errors.Is` → `true`).
- M08 MG `override`: убрать `setState(id, domain.StateDone)` в ручной ветке.
- M09 CB `dispatch`: в ветке `!claimed` убрать проверку `IsIncidentCard` (сразу `already decided`).
- M10 CB `dispatch`: в ветке `!claimed` инвертировать `!current` → `current`.
- M11 CB `staleCard`: убрать вызов `EditAdminMarkup`.
- M12 CB `dispatch`: в ветке `!claimed` при ошибке `IsIncidentCard` вернуть `already decided` вместо ошибки.
- M13 CB `dispatch`: в ветке `!claimed`, при неактуальной карточке, перед ответом вызвать `h.releaseClaim(inc.ID, act)`.

Для выживших: одна строка — какой тест/проверка должна была его поймать и почему не поймала (или почему мутант
эквивалентен). Тесты НЕ дописывай.

## 1a. Что проверено вживую, а что предположение

Проверено постановщиком 05.10.2026: целевая команда зелёная на BASE (полный `-race` сьют тоже); все
названные функции (`override`, `sendCard`, `process`, `dispatch`, `staleCard`) и `errCardNotRecorded`
есть в BASE. Предположений нет.

## 1b. Не трогать

Тесты (`*_test.go`), `go.mod`, `go.sum`, всё вне двух боевых файлов; каждая мутация откатывается до
следующей. Живой Telegram, прод, host tmux.

## 2. Контракт отчёта

`tmp/mutation-report.md` — таблица `id | файл:строка | исход | комментарий`, итог killed/survived/invalid.
`report.json` в корне клона (untracked):

```json
{"criteria": [{"id": "AC-001", "status": "pass|fail|blocked", "command": "…", "rc": 0, "note": "…"}]}
```

## 3. Критерии приёмки

- **AC-001.** Отчёт о мутантах есть и покрывает все тринадцать:
  `bash -c 'for i in 01 02 03 04 05 06 07 08 09 10 11 12 13; do grep -q "M$i" tmp/mutation-report.md || exit 1; done'`
- **AC-002.** Боевой код и тесты после прогона не изменены, коммитов нет:
  `bash -c 'git diff --quiet && test "$(git rev-parse HEAD)" = 9e575036213304e4cbc28105501d50b39c4b4c26'`

## 4. Контракт на невыполнимое

Целевая команда красная на чистом дереве, мутант нельзя однозначно внести — стоп, `report-blocked.md` с
дословной командой и выводом. Запрещено: правка тестов, `git push`, коммиты, `|| true` в критериях, `sudo`.
Авторевью в этой задаче не запускается — это проверка, ревью делает координатор.
