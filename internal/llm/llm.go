// Package llm задаёт общий интерфейс для любого движка (Claude, GigaChat и т.п.),
// чтобы остальной код бота (botapp) не зависел от конкретного провайдера.
package llm

import "context"

// Client — общий интерфейс ИИ-провайдера с поддержкой текста и фото.
type Client interface {
	// AskWithPhoto отправляет вопрос вместе с фото (JPEG/PNG) и системным промптом.
	AskWithPhoto(ctx context.Context, systemPrompt, question string, photo []byte, mediaType string) (string, error)
	// AskText отправляет обычный текстовый вопрос без фото.
	AskText(ctx context.Context, systemPrompt, question string) (string, error)
}
