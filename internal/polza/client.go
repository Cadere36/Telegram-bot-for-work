// Package polza — клиент к Polza.ai (российский агрегатор моделей, включая Claude),
// использующему OpenAI-совместимый формат запросов.
//
// Документация: https://polza.ai/docs
//
// Формат такой же, как у OpenAI/OpenRouter: эндпоинт /chat/completions,
// системный промпт — сообщение с ролью "system", фото передаётся как
// content-блок "image_url" с data URI (data:image/jpeg;base64,...).
package polza

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const apiURL = "https://polza.ai/api/v1/chat/completions"

// Client — клиент Polza.ai.
type Client struct {
	apiKey     string
	model      string // точный ID модели смотрите на polza.ai/models, например "anthropic/claude-3-5-sonnet"
	httpClient *http.Client
}

// New создаёт клиента Polza.ai.
func New(apiKey, model string) *Client {
	return &Client{
		apiKey: apiKey,
		model:  model,
		httpClient: &http.Client{
			Timeout: 90 * time.Second,
		},
	}
}

type contentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *imageURL `json:"image_url,omitempty"`
}

type imageURL struct {
	URL string `json:"url"`
}

type message struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"` // строка (текст) или []contentPart (текст+фото)
}

type requestBody struct {
	Model    string    `json:"model"`
	Messages []message `json:"messages"`
}

type responseBody struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// AskWithPhoto отправляет вопрос вместе с фото и системным промптом.
func (c *Client) AskWithPhoto(ctx context.Context, systemPrompt, question string, photo []byte, mediaType string) (string, error) {
	dataURI := fmt.Sprintf("data:%s;base64,%s", mediaType, base64.StdEncoding.EncodeToString(photo))

	messages := []message{
		{Role: "system", Content: systemPrompt},
		{
			Role: "user",
			Content: []contentPart{
				{Type: "text", Text: question},
				{Type: "image_url", ImageURL: &imageURL{URL: dataURI}},
			},
		},
	}

	return c.send(ctx, messages)
}

// AskText отправляет обычный текстовый вопрос без фото.
func (c *Client) AskText(ctx context.Context, systemPrompt, question string) (string, error) {
	messages := []message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: question},
	}
	return c.send(ctx, messages)
}

func (c *Client) send(ctx context.Context, messages []message) (string, error) {
	reqBody := requestBody{
		Model:    c.model,
		Messages: messages,
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	var parsed responseBody
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("unmarshal response (status %d): %w, body: %s", resp.StatusCode, err, string(body))
	}

	if parsed.Error != nil {
		return "", fmt.Errorf("polza.ai error: %s", parsed.Error.Message)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("polza.ai вернул статус %d: %s", resp.StatusCode, string(body))
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("пустой ответ от polza.ai")
	}

	return parsed.Choices[0].Message.Content, nil
}
