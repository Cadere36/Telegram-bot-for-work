// Package claude — минимальный клиент к Claude API (Anthropic Messages API).
// Сделан без стороннего SDK, чтобы не тащить лишние зависимости в MVP —
// это обычный HTTP POST с JSON, ничего специфичного под Go тут нет.
package claude

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

const apiURL = "https://api.anthropic.com/v1/messages"
const apiVersion = "2023-06-01"

// Client — клиент Claude API.
type Client struct {
	apiKey     string
	model      string
	httpClient *http.Client
}

// New создаёт клиента с заданным ключом и моделью.
func New(apiKey, model string) *Client {
	return &Client{
		apiKey: apiKey,
		model:  model,
		httpClient: &http.Client{
			// Vision-запрос с большой нормативной базой в контексте может
			// обрабатываться дольше обычного текстового — даём запас.
			Timeout: 90 * time.Second,
		},
	}
}

// contentBlock — один блок содержимого сообщения: текст или изображение.
type contentBlock struct {
	Type   string      `json:"type"`
	Text   string      `json:"text,omitempty"`
	Source *imageSource `json:"source,omitempty"`
}

type imageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type message struct {
	Role    string         `json:"role"`
	Content []contentBlock `json:"content"`
}

type requestBody struct {
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	System    string    `json:"system,omitempty"`
	Messages  []message `json:"messages"`
}

type responseBody struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// AskWithPhoto отправляет вопрос пользователя вместе с фотографией (JPEG/PNG) и
// системным промптом (правила ответа + релевантные нормы ПБ) и возвращает текст ответа модели.
func (c *Client) AskWithPhoto(ctx context.Context, systemPrompt, question string, photo []byte, mediaType string) (string, error) {
	blocks := []contentBlock{
		{
			Type: "image",
			Source: &imageSource{
				Type:      "base64",
				MediaType: mediaType,
				Data:      base64.StdEncoding.EncodeToString(photo),
			},
		},
		{Type: "text", Text: question},
	}
	return c.send(ctx, systemPrompt, blocks)
}

// AskText отправляет обычный текстовый вопрос (без фото).
func (c *Client) AskText(ctx context.Context, systemPrompt, question string) (string, error) {
	blocks := []contentBlock{{Type: "text", Text: question}}
	return c.send(ctx, systemPrompt, blocks)
}

func (c *Client) send(ctx context.Context, systemPrompt string, blocks []contentBlock) (string, error) {
	reqBody := requestBody{
		Model:     c.model,
		MaxTokens: 1500,
		System:    systemPrompt,
		Messages: []message{
			{Role: "user", Content: blocks},
		},
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
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", apiVersion)

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
		return "", fmt.Errorf("claude api error (%s): %s", parsed.Error.Type, parsed.Error.Message)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("claude api вернул статус %d: %s", resp.StatusCode, string(body))
	}
	if len(parsed.Content) == 0 {
		return "", fmt.Errorf("пустой ответ от claude api")
	}

	return parsed.Content[0].Text, nil
}
