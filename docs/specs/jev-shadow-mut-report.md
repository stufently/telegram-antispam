# Мутации jev-shadow

HEAD `976ee2cdb470f00f4c5a2fcf9ae8105ffed0f453`. Перед мутантами целевая команда на чистом дереве зелёная (`internal/llm` 1.355s, `internal/config` 2.497s, `cmd/tg-antispam` 2.407s, rc 0).

Команда каждого мутанта: `./scripts/dev.sh test -race -count=1 ./internal/llm/ ./internal/config/ ./cmd/tg-antispam/`

После каждого прогона файл возвращён через `git checkout -- <файл>`, `git diff --quiet` чистый. Коммитов нет.

Итог: killed 27, survived 0, invalid 0.

| id | файл:строка | исход | тест | комментарий |
|---|---|---|---|---|
| M01 | internal/llm/llm.go:126 | killed | TestAdjudicateEachPerProviderPrompt, TestShadowReportsVotes | Провайдер i получает prompts[0]. Тесты ждут свой промпт у второго провайдера. |
| M02 | internal/llm/llm.go:143 | killed | TestShadowReportsVotes, TestLLMStageShadowLog | Сравнение с Cutoff строгое. Равенство 0.42 порогу больше не даёт spam. |
| M03 | internal/llm/llm.go:138 | killed | TestShadowReportsVotes, TestLLMStageShadowLog | Ветка Scorer убрана, всегда Classify, Score остаётся nil. |
| M04 | internal/llm/llm.go:182 | killed | TestAdjudicateVotes, TestShadowTimeout, TestShadowSlotHeldUntilProvidersReturn | К дедлайну у неответивших Err не проставляется. Тесты ждут context.Canceled / DeadlineExceeded. |
| M05 | internal/llm/llm.go:123 | killed | TestAdjudicateVotes, TestShadowReportsVotes, TestLLMStageMetrics, TestLLMStageShadowLog | Имя провайдера в Vote пустое. Метрики и лог ищут его. |
| M06 | internal/llm/shadow.go:28 | killed | TestShadowConcurrentAdmission | При Max <= 0 пул 1000 вместо 1. Для max -1 и 0 принято 32 при ожидании 1. |
| M07 | internal/llm/shadow.go:30 | killed | TestShadowDropsWhenFull, TestShadowConcurrentAdmission, TestShadowSlotHeldUntilProvidersReturn, TestLLMStageMetrics | Условие active > max пускает наблюдение в уже полный пул. |
| M08 | internal/llm/shadow.go:40 | killed | TestShadowSlotHeldUntilProvidersReturn | calls.Wait() убран из defer. Слот освобождается сразу после report, пока провайдер ещё выполняется. |
| M09 | internal/llm/shadow.go:52 | killed | TestShadowSlotHeldUntilProvidersReturn | В adjudicateEach передан nil. WaitGroup пустой, слот не держится до возврата провайдера. |
| M10 | internal/llm/shadow.go:47 | killed | TestShadowZeroTimeoutUsesDefault | Подстановка defaultShadowTimeout убрана. Timeout 0 и -1 дают уже истёкший контекст. |
| M11 | internal/llm/shadow.go:46 | killed | TestShadowTimeout, TestShadowSlotHeldUntilProvidersReturn | Контекст равен parent, без дедлайна. Локальная timeout убрана вместе с WithTimeout: иначе она не используется и файл не компилируется. Оба теста ждут отчёт 2 с и получают report timeout. |
| M12 | internal/llm/jev.go:39 | killed | TestJEVRequestAndThreshold, TestJEVScoreAndCutoff | Classify считает спамом только p > Threshold. При p == 0.42 и пороге 0.42 ответ ham. |
| M13 | internal/llm/jev.go:43 | killed | TestJEVScoreAndCutoff, TestConfiguredJEVProvider | Cutoff всегда 0.30. Тесты сверяют его с настроенным Threshold. |
| M14 | internal/config/config.go:419 | killed | TestProviderPromptForPrecedence, TestLLMStagePerProviderPrompt | p.Prompt идёт раньше чатового override. Возвращается «provider», тесты ждут «provider chat» / «shadow chat». |
| M15 | internal/config/config.go:419 | killed | TestProviderPromptForPrecedence | Третьего уровня l.PromptFor(chatID) нет. После пустых промптов провайдера тест ждёт «global chat». |
| M16 | internal/config/config.go:993 | killed | TestLLMProviderPromptCoversChatOverrides | Проверка покрытия prompt_overrides удалена. Пустой override чата проходит Validate. |
| M17 | internal/config/config.go:993 | killed | TestLLMProviderPromptCoversChatOverrides | Покрытие проверяется только при p.Shadow. Голосующий провайдер теста проверку пропускает. |
| M18 | internal/config/config.go:992 | killed | TestLLMRequiresVotingProvider, TestLLMEnabledValid, TestLLMAPIKeysComeFromEnv, TestLLMTuningParamsPreserveUnsetVersusZero, TestLLMPromptOverridePerChat, TestJEVConfigDefaultsAndEnvironment, TestLLMProviderPromptCoversChatOverrides, TestConfiguredJEVProvider | voting копится от p.Shadow. Обычный провайдер больше не голосующий, и каждый включённый конфиг из одних голосующих падает с «llm requires at least one voting provider». |
| M19 | cmd/tg-antispam/llm_stage.go:39 | killed | TestLLMStageShadowNeverVotes, TestLLMStageMetrics, TestLLMStageShadowLog, TestLLMStagePerProviderPrompt | Теневой провайдер добавлен ещё и в judge.Providers. TestLLMStageShadowNeverVotes: модерация ждёт заблокированную тень и падает с «shadow blocked moderation». |
| M20 | cmd/tg-antispam/llm_stage.go:82 | killed | TestLLMStageMetrics | При Failed >= Total метка остаётся boolLabel, не «error». Кейс error/spam ждёт result=error. |
| M21 | cmd/tg-antispam/llm_stage.go:94 | killed | TestLLMStageMetrics, TestLLMStageShadowLog | Observe получает уже отменённый cctx. Тень отвечает error, строка dropped не появляется. |
| M22 | cmd/tg-antispam/llm_stage.go:94 | killed | TestLLMStageMetrics | Подсчёт dropped при !accepted убран. Присваивание accepted убрано вместе с ним, иначе переменная не используется. Тест ждёт result=dropped. |
| M23 | cmd/tg-antispam/llm_stage.go:98 | killed | TestLLMStageMetrics | В tg_antispam_llm_shadow_agreement_total значения voter и shadow переставлены. |
| M24 | cmd/tg-antispam/llm_stage.go:103 | killed | TestLLMStageShadowLog | В строку лога добавлен text=%q с текстом сообщения. Тест сверяет строку целиком, без текста. |
| M25 | cmd/tg-antispam/llm_stage.go:94 | killed | TestLLMStagePerProviderPrompt | Промпты тени считаются по voteIndices. Тень получает «vote chat» вместо «shadow chat». |
| M26 | cmd/tg-antispam/llm_stage.go:96 | killed | TestLLMStageShadowNeverVotes | При !v.OK в report тени растёт tg_antispam_llm_provider_failures_total. Тест запрещает этот счётчик на ошибке тени. |
| M27 | cmd/tg-antispam/llm_stage.go:90 | killed | TestLLMStageMetrics | Роль голосующих записана как shadow. Тест ждёт role=vote. |

Выживших нет. Каждый мутант менял наблюдаемое поведение, и хотя бы один тест целевой команды упал на этом отличии.
