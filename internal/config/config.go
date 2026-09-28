// Package config загружает настройки приложения из переменных окружения (.env для локальной разработки).
package config

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

// Config содержит все настройки, нужные боту для работы.
type Config struct {
	TelegramToken  string // токен бота, полученный от @BotFather
	ClaudeAPIKey   string // ключ Claude API (console.anthropic.com)
	ClaudeModel    string // модель, например claude-sonnet-4-5-20250929
	DatabaseURL    string // строка подключения к Postgres, например postgres://user:pass@localhost:5432/pbbot?sslmode=disable
	AllowedUserIDs string // (опционально) список Telegram user_id через запятую, кому разрешено пользоваться ботом
	LLMProvider            string
	GigaChatAuthKey        string
	GigaChatScope          string
	GigaChatModel          string
	GigaChatInsecureSkipVerify bool
	PolzaAPIKey	string 
	PolzaModel string 
}

// Load читает .env (если есть) и переменные окружения, проверяет обязательные поля.
func Load() (*Config, error) {
	// .env нужен только для локальной разработки; в проде переменные обычно задаются
	// через systemd unit / docker-compose, поэтому отсутствие файла — не ошибка.
	_ = godotenv.Load()

	cfg := &Config{
		TelegramToken:  os.Getenv("TELEGRAM_BOT_TOKEN"),
		ClaudeAPIKey:   os.Getenv("CLAUDE_API_KEY"),
		ClaudeModel:    getEnvDefault("CLAUDE_MODEL", "claude-sonnet-4-5-20250929"),
		DatabaseURL:    os.Getenv("DATABASE_URL"),
		AllowedUserIDs: os.Getenv("ALLOWED_TELEGRAM_USER_IDS"),
		LLMProvider:     getEnvDefault("LLM_PROVIDER", "claude"),
	GigaChatAuthKey: os.Getenv("GIGACHAT_AUTH_KEY"),
	GigaChatScope:   getEnvDefault("GIGACHAT_SCOPE", "GIGACHAT_API_PERS"),
	GigaChatModel:   getEnvDefault("GIGACHAT_MODEL", "GigaChat"),
	GigaChatInsecureSkipVerify: os.Getenv("GIGACHAT_INSECURE_SKIP_VERIFY") == "true",
	PolzaAPIKey: os.Getenv("POLZA_API_KEY"),
	PolzaModel: getEnvDefault("POLZA_MODEL", "anthropic/claude-4-5-sonnet"),
	}

	if cfg.TelegramToken == "" {
		return nil, fmt.Errorf("не задан TELEGRAM_BOT_TOKEN")
	}
	switch cfg.LLMProvider {
case "gigachat":
	if cfg.GigaChatAuthKey == "" {
		return nil, fmt.Errorf("не задан GIGACHAT_AUTH_KEY")
	}
case "polza":
	if cfg.PolzaAPIKey == "" {
		return nil, fmt.Errorf("не задан POLZA_API_KEY")
	}
default:
	if cfg.ClaudeAPIKey == "" {
		return nil, fmt.Errorf("не задан CLAUDE_API_KEY")
	}
}
	// DatabaseURL пока не обязателен: на первом шаге (эхо-бот с vision-анализом
	// без базы нормативки) бот может работать и без Postgres.

	return cfg, nil
}

func getEnvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
