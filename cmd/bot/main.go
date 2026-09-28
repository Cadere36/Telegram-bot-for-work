// Команда bot запускает Telegram-бота для анализа фото и вопросов по пожарной
// безопасности через Claude API.
package main

import (
	"context"
	"log"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"pb-bot/internal/botapp"
	"pb-bot/internal/claude"
	"pb-bot/internal/config"
	"pb-bot/internal/store"
	"pb-bot/internal/gigachat"
	"pb-bot/internal/llm"
	"pb-bot/internal/polza"
	"pb-bot/internal/rag"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("ошибка конфигурации: %v", err)
	}

	bot, err := tgbotapi.NewBotAPI(cfg.TelegramToken)
	if err != nil {
		log.Fatalf("не удалось подключиться к Telegram: %v", err)
	}
	log.Printf("авторизован как @%s", bot.Self.UserName)

	var aiClient llm.Client

switch cfg.LLMProvider {
case "gigachat":
	gc, err := gigachat.New(cfg.GigaChatAuthKey, cfg.GigaChatScope, cfg.GigaChatModel, gigachat.Options{
		InsecureSkipVerify: cfg.GigaChatInsecureSkipVerify,
	})
	if err != nil {
		log.Fatalf("не удалось создать gigachat клиента: %v", err)
	}
	
	aiClient = gc
case "polza":
	aiClient = polza.New(cfg.PolzaAPIKey, cfg.PolzaModel)
default:
	aiClient = claude.New(cfg.ClaudeAPIKey, cfg.ClaudeModel)
}

	var st *store.Store
	if cfg.DatabaseURL != "" {
		st, err = store.Open(cfg.DatabaseURL)
		if err != nil {
			log.Fatalf("не удалось подключиться к БД: %v", err)
		}
		defer st.Close()

		if err := st.Migrate(context.Background()); err != nil {
			log.Fatalf("ошибка миграции БД: %v", err)
		}
		log.Println("подключение к БД установлено, миграции применены")
	} else {
		log.Println("DATABASE_URL не задан — бот работает без логирования в БД и без базы нормативки")
	}

	// searcher = nil -> используется rag.NoopSearcher (шаг 1: без базы нормативки).
	var searcher rag.Searcher
if cfg.LLMProvider == "polza" && st != nil {
	embedder := polza.New(cfg.PolzaAPIKey, "")
	searcher = rag.NewPgSearcher(st, embedder)
}
app := botapp.New(bot, aiClient, searcher, st, cfg.AllowedUserIDs)
	app.Run()
}
