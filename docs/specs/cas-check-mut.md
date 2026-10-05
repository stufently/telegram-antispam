# Мутационная проверка новых тестов вехи cas-check

- Репозиторий: клон `/home/deploy/exec-clones/antispam-cas-check-mut-20261005`, ветка `cas-check`,
  BASE `8710525001350edf8f50d37c781721ca4c25e6e8`. Спека лежит вне клона, в клон её не копируй и не коммить.
- Исполнитель: `gk` (Grok). Код не исправляешь, коммитов не делаешь, push запрещён (push-url `no-push`).
- Критериев: 2.

## 1. Задача

Проверить, ловят ли новые и переписанные тесты (`internal/blocklist/cas_test.go`,
`internal/blocklist/sync_test.go`, `internal/config/cas_config_test.go`) поломки боевого кода.
Для каждого мутанта ниже по одному: внести правку в боевой файл → прогнать целевую команду → записать
исход (killed = команда упала из-за теста; survived = зелёная; invalid = не компилируется) → вернуть файл
`git checkout -- <файл>` и убедиться в `git diff --quiet`. Перед первым мутантом прогони целевую команду на
чистом дереве: она обязана быть зелёной.

Целевая команда (из корня клона):
`./scripts/dev.sh test -count=1 ./internal/blocklist/ ./internal/config/`

Мутанты (`internal/blocklist/cas.go` = CAS, `internal/blocklist/sync.go` = SY, `internal/config/config.go` = CF):

- M01 CAS `Listed`: убрать охрану `userID <= 0`.
- M02 CAS `Listed`: условие попадания в кэш `now.Before(entry.expires)` → `!now.After(entry.expires)`.
- M03 CAS `Listed`: для listed брать `NegativeTTL` (оба TTL = `NegativeTTL`).
- M04 CAS `Listed`: при ошибке класть в кэш `false` на `NegativeTTL` (кэшировать ошибки).
- M05 CAS: `casBreakerErrors` 3 → 4.
- M06 CAS: `casBreakerPause` 60 с → 30 с.
- M07 CAS `Listed`: успешный ответ не обнуляет `c.errors`.
- M08 CAS `Listed`: убрать проверку лимитера (всегда идти в сеть).
- M09 CAS `check`: вместо замены query добавлять `user_id` к имеющемуся (`q := u.Query(); q.Set("user_id", …); u.RawQuery = q.Encode()`).
- M10 CAS `check`: убрать проверку статуса не-2xx.
- M11 CAS `check`: при `result.OK == nil` вернуть `false, nil` вместо ошибки.
- M12 CAS `check`: убрать проверку достижения `casBodyLimit`.
- M13 CAS `anyOf.Listed`: без короткого замыкания (опросить все, вернуть OR).
- M14 CAS `Listed`: при полном кэше не вычищать истёкшие записи.
- M15 CAS `Listed`: класть в кэш всегда, без проверки `MaxEntries`.
- M16 CAS `check`: таймаут контекста `c.cfg.Timeout` → `10 * c.cfg.Timeout`.
- M17 CAS `Listed`: при ошибке не вызывать `c.count("error")`.
- M18 CF `applyBlocklistDefaults`: дефолт `CasCheckEnabled` → `false`.
- M19 CF `applyBlocklistDefaults`: дефолт `CasNegativeTTL` 6h → 24h.
- M20 CF `applyBlocklistDefaults`: условие `CasBurst <= 0` → `CasBurst == 0`.
- M21 SY `RefreshFull`: убрать охрану пустого списка (`len(ids) == 0`).
- M22 SY `RefreshFull`: не сбрасывать `b.lolsDelta = nil`.

Для выживших: одна строка — какой тест/проверка должна была его поймать и почему не поймала (или почему мутант
эквивалентен). Тесты НЕ дописывай.

## 1a. Что проверено вживую, а что предположение

Проверено постановщиком 05.10.2026: целевая команда зелёная на BASE; все названные функции и константы
(`Listed`, `check`, `anyOf`, `casBreakerErrors`, `casBreakerPause`, `casBodyLimit`, `applyBlocklistDefaults`,
`RefreshFull`) есть в BASE. Предположений нет.

## 1b. Не трогать

Тесты (`*_test.go`), `go.mod`, `go.sum`, всё вне трёх боевых файлов; каждая мутация откатывается до
следующей. Живой Telegram, прод, host tmux, реальная сеть кроме модульного прокси `./scripts/dev.sh`.

## 2. Контракт отчёта

`tmp/mutation-report.md` — таблица `id | файл:строка | исход | комментарий`, итог killed/survived/invalid.
`report.json` в корне клона (untracked):

```json
{"criteria": [{"id": "AC-001", "status": "pass|fail|blocked", "command": "…", "rc": 0, "note": "…"}]}
```

## 3. Критерии приёмки

- **AC-001.** Отчёт о мутантах есть и покрывает все двадцать два:
  `bash -c 'for i in 01 02 03 04 05 06 07 08 09 10 11 12 13 14 15 16 17 18 19 20 21 22; do grep -q "M$i" tmp/mutation-report.md || exit 1; done'`
- **AC-002.** Боевой код и тесты после прогона не изменены, коммитов нет:
  `bash -c 'git diff --quiet && test "$(git rev-parse HEAD)" = 8710525001350edf8f50d37c781721ca4c25e6e8'`

## 4. Контракт на невыполнимое

Целевая команда красная на чистом дереве, мутант нельзя однозначно внести — стоп, `report-blocked.md` с
дословной командой и выводом. Запрещено: правка тестов, `git push`, коммиты, `|| true` в критериях, `sudo`.
Авторевью в этой задаче не запускается — это проверка, ревью делает координатор.
