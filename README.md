# pb-bot — MVP-бот по пожарной безопасности

Telegram-бот: коллеги присылают фото объекта или задают вопрос, бот отвечает
через Claude API с оговоркой, что это предварительная оценка, а не акт проверки.

Это **шаг 1** из плана внедрения — бот без базы нормативки (RAG), проверяет саму
связку Telegram → Claude Vision → ответ. Шаг 2 (загрузка НПА и поиск по ним) —
это `internal/rag`, которая уже подготовлена как интерфейс, но пока не подключена.

## Структура проекта

```
cmd/bot/main.go        — точка входа
internal/config         — загрузка настроек из .env / переменных окружения
internal/claude          — клиент Claude API (HTTP, без сторонних SDK)
internal/botapp          — обработчик сообщений Telegram, системный промпт
internal/store           — логирование в Postgres, схема БД (задел под RAG)
internal/rag              — интерфейс поиска по нормативке (шаг 2, пока заглушка)
migrations/               — SQL-схема (дублируется в store.go для авто-применения)
deploy/                   — пример systemd unit для сервера организации
docker-compose.yml        — Postgres с pgvector для локального запуска
```

## Быстрый старт (локально, для проверки)

1. Получить токен бота у `@BotFather` в Telegram.
2. Получить ключ Claude API: https://console.anthropic.com/settings/keys
3. Скопировать `.env.example` в `.env` и заполнить `TELEGRAM_BOT_TOKEN` и `CLAUDE_API_KEY`.
   `DATABASE_URL` на первом шаге можно оставить пустым — бот заработает без БД
   (просто не будет логировать обращения).
4. Собрать и запустить:

```bash
go build -o bin/pb-bot ./cmd/bot
./bin/pb-bot
```

5. Написать боту в Telegram: прислать фото с подписью-вопросом или просто текст.

## С логированием в Postgres (рекомендуется сразу)

```bash
docker compose up -d postgres
```

Указать в `.env`:
```
DATABASE_URL=postgres://pbbot:pbbot@localhost:5432/pbbot?sslmode=disable
```

При старте бот сам создаст таблицу `interaction_log` (миграция в `store.go`).
Каждое обращение (кто спросил, был ли фото, что ответила модель) сохраняется —
это пригодится и для разбора ошибок, и как след для аудита.

## Деплой на сервер организации

1. Собрать бинарник под Linux (если разрабатываете не на Linux):
   ```bash
   GOOS=linux GOARCH=amd64 go build -o bin/pb-bot ./cmd/bot
   ```
2. Скопировать `bin/pb-bot` и `.env` на сервер, например в `/opt/pb-bot/`.
3. Поднять Postgres на сервере (`docker compose up -d postgres`, либо
   использовать уже существующий Postgres организации — тогда просто указать
   его `DATABASE_URL` и один раз выполнить `CREATE EXTENSION IF NOT EXISTS vector;`
   когда дойдём до шага 2 с базой нормативки).
4. Настроить автозапуск через systemd — пример в `deploy/pb-bot.service.example`.
5. Проверить исходящий доступ с сервера к `api.telegram.org` и `api.anthropic.com`
   (бот работает в режиме polling, входящие подключения не нужны — только исходящие).

## Что дальше (шаг 2)

- Загрузить нормативные документы (ПБ) и локальные приказы организации.
- Реализовать `rag.Searcher` (сейчас — `rag.NoopSearcher`, всегда пустой контекст):
  - выбрать провайдера эмбеддингов (у Anthropic нет своего embeddings API,
    обычно берут Voyage AI — партнёр Anthropic, либо OpenAI embeddings);
  - разбить документы на смысловые куски и получить для них эмбеддинги;
  - сохранить в Postgres (`document_chunks.embedding`, закомментировано в
    `store.go` — раскомментировать после `CREATE EXTENSION vector`);
  - при вопросе — искать ближайшие куски по косинусному расстоянию и
    передавать их в `rag.BuildContext`.
- Ограничить круг пользователей бота (`ALLOWED_TELEGRAM_USER_IDS` уже
  прокинут в конфиг, но фильтрация в `botapp` пока не включена — добавить
  проверку `msg.From.ID` в начале `handleMessage`, когда определитесь со
  списком).
