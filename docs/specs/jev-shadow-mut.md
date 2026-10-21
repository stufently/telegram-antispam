# Мутационная проверка новых тестов вехи jev-shadow

- Репозиторий: клон `/home/deploy/exec-clones/antispam-jev-shadow-mut-20261021`, ветка `jev-shadow`,
  HEAD `976ee2cdb470f00f4c5a2fcf9ae8105ffed0f453`. Спека лежит вне клона, в клон её не копируй и не коммить.
- Исполнитель: `gk` (Grok) — код вехи писал Codex, мутации гоняет противоположный исполнитель.
  Код не исправляешь, коммитов не делаешь, push запрещён (push-url `no-push`).
- Критериев: 2.

## 1. Задача

Проверить, ловят ли новые тесты вехи (`internal/llm/shadow_test.go`,
`internal/config/llm_shadow_test.go`, `cmd/tg-antispam/llm_stage_test.go`) поломки боевого кода.
Для каждого мутанта ниже по одному: внести правку в боевой файл → прогнать целевую команду → записать
исход (killed = команда упала из-за теста; survived = зелёная; invalid = не компилируется) → вернуть файл
`git checkout -- <файл>` и убедиться в `git diff --quiet`. Перед первым мутантом прогони целевую команду на
чистом дереве: она обязана быть зелёной. Для killed запиши имя упавшего теста.

Целевая команда (из корня клона):
`./scripts/dev.sh test -race -count=1 ./internal/llm/ ./internal/config/ ./cmd/tg-antispam/`

Мутанты (`internal/llm/llm.go` = LL, `internal/llm/shadow.go` = SH, `internal/llm/jev.go` = JV,
`internal/config/config.go` = CF, `cmd/tg-antispam/llm_stage.go` = ST):

- M01 LL `adjudicateEach`: провайдер `i` получает `prompts[0]` вместо `prompts[i]` (при совпадении длин).
- M02 LL `adjudicateEach`: ветка `Scorer` — `spam = value > scorer.Cutoff()` (строгое).
- M03 LL `adjudicateEach`: ветку `Scorer` убрать, всегда `Classify` (`Score` остаётся nil).
- M04 LL `adjudicateEach`: в ветке дедлайна не проставлять `out.Votes[i].Err` неответившим.
- M05 LL `adjudicateEach`: не заполнять `out.Votes[i].Provider`.
- M06 SH `Observe`: при `Max <= 0` вместо 1 брать 1000.
- M07 SH `Observe`: условие `s.active >= max` → `s.active > max`.
- M08 SH `Observe`: убрать `calls.Wait()` из defer (слот снимается сразу после report).
- M09 SH `Observe`: передавать `nil` вместо `&calls` в `adjudicateEach`.
- M10 SH `Observe`: `timeout <= 0` → оставить 0 (убрать подстановку `defaultShadowTimeout`).
- M11 SH `Observe`: контекст без таймаута (`ctx := parent`, без `WithTimeout`).
- M12 JV `Classify`: `p >= j.Threshold` → `p > j.Threshold`.
- M13 JV `Cutoff`: вернуть константу `0.30`.
- M14 CF `ProviderPromptFor`: порядок — `p.Prompt` раньше `p.PromptOverrides[chatID]`.
- M15 CF `ProviderPromptFor`: убрать третий уровень (`l.PromptFor(chatID)`).
- M16 CF `Validate`: убрать проверку покрытия `prompt_overrides`.
- M17 CF `Validate`: проверку покрытия делать только при `p.Shadow`.
- M18 CF `Validate`: `voting = voting || !p.Shadow` → `voting = voting || p.Shadow`.
- M19 ST `newLLMStage`: теневых провайдеров добавлять ещё и в `s.judge.Providers`.
- M20 ST `adjudicate`: убрать присваивание `voter = "error"` при `out.Failed >= out.Total`.
- M21 ST `adjudicate`: в `Observe` передавать `cctx` (уже отменённый) вместо `ctx`.
- M22 ST `adjudicate`: убрать подсчёт `dropped` при `!accepted`.
- M23 ST `adjudicate`: в `tg_antispam_llm_shadow_agreement_total` поменять местами значения `voter` и `shadow`.
- M24 ST `adjudicate`: в строку лога тени добавить `text=%q` с текстом сообщения.
- M25 ST `adjudicate`: промпты тени считать по `s.voteIndices` вместо `s.shadowIndices`.
- M26 ST `adjudicate`: в `report` тени при `!v.OK` увеличивать `tg_antispam_llm_provider_failures_total`.
- M27 ST `adjudicate`: метку роли голосующих `"vote"` → `"shadow"`.

Для выживших: одна строка — какой тест должен был его поймать и почему не поймал (или почему мутант
эквивалентен, с обоснованием по наблюдаемому поведению). Тесты НЕ дописывай.

## 1a. Что проверено вживую, а что предположение

Проверено постановщиком 21.10.2026 на HEAD `976ee2c`: машинная приёмка вехи зелёная (8/8), все
названные функции и идентификаторы (`adjudicateEach`, `Scorer`, `Cutoff`, `Observe`, `calls.Wait`,
`defaultShadowTimeout`, `ProviderPromptFor`, `Validate`, `newLLMStage`, `adjudicate`, `voteIndices`,
`shadowIndices`, метрики `tg_antispam_llm_*`) есть в коде. Предположений нет.

## 1b. Не трогать

Тесты (`*_test.go`), `go.mod`, `go.sum`, всё вне пяти боевых файлов; каждая мутация откатывается до
следующей. Живой Telegram, прод, host tmux, реальная сеть кроме модульного прокси `./scripts/dev.sh`.

## 2. Контракт отчёта

`tmp/mutation-report.md` — таблица `id | файл:строка | исход | тест | комментарий`, итог
killed/survived/invalid. `report.json` в корне клона (untracked):

```json
{"criteria": [{"id": "AC-001", "status": "pass|fail|blocked", "command": "…", "rc": 0, "note": "…"}]}
```

## 3. Критерии приёмки

- **AC-001.** Отчёт о мутантах есть и покрывает все двадцать семь:
  `bash -c 'for i in 01 02 03 04 05 06 07 08 09 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27; do grep -q "M$i" tmp/mutation-report.md || exit 1; done'`
- **AC-002.** Боевой код и тесты после прогона не изменены, коммитов нет:
  `bash -c 'git diff --quiet && test "$(git rev-parse HEAD)" = 976ee2cdb470f00f4c5a2fcf9ae8105ffed0f453'`

## 4. Контракт на невыполнимое

Целевая команда красная на чистом дереве, мутант нельзя однозначно внести — стоп, `report-blocked.md` с
дословной командой и выводом. Запрещено: правка тестов, `git push`, коммиты, `|| true` в критериях, `sudo`.
Авторевью в этой задаче не запускается — это проверка, ревью делает координатор.
