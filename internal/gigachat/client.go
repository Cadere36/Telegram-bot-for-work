// Package gigachat — клиент к GigaChat API (Сбер) как альтернатива Claude API
// для оплаты в рублях без карты иностранного банка.
//
// Документация: https://developers.sber.ru/docs/ru/gigachat/api/reference/rest/gigachat-api
//
// Особенности по сравнению с Claude API:
//   - Авторизация не по статичному ключу, а через OAuth: раз в ~30 минут
//     нужно менять "ключ авторизации" на временный access_token.
//   - Фото нельзя просто вставить в сообщение base64 — сначала его нужно
//     загрузить через /files и получить file_id, потом сослаться на него
//     в attachments.
//   - У GigaChat самоподписанный сертификат (корневой УЦ Минцифры России).
//     Обычный http.Client его не примет ("x509: certificate signed by
//     unknown authority"). Варианты:
//       1) Один раз поставить сертификат Минцифры в систему (рекомендуется
//          для сервера в проде): https://www.gosuslugi.ru/crt
//       2) Указать путь к скачанному .pem через GIGACHAT_CA_CERT_PATH — тогда
//          клиент будет доверять именно ему, не отключая проверку целиком.
//       3) Для быстрого локального теста — GIGACHAT_INSECURE_SKIP_VERIFY=true
//          (отключает проверку сертификата вовсе, НЕ использовать в проде).
package gigachat

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"sync"
	"time"
	"net/textproto"

	"github.com/google/uuid"
)

const (
	oauthURL = "https://ngw.devices.sberbank.ru:9443/api/v2/oauth"
	apiBase  = "https://gigachat.devices.sberbank.ru/api/v1"
)

// Client — клиент GigaChat API.
type Client struct {
	authKey string // "ключ авторизации" из личного кабинета developers.sber.ru (Basic-токен, уже в base64)
	scope   string // GIGACHAT_API_PERS (физлицо) или GIGACHAT_API_CORP / GIGACHAT_API_B2B (юрлицо)
	model   string // например "GigaChat", "GigaChat-Pro", "GigaChat-Max"

	httpClient *http.Client

	mu          sync.Mutex
	accessToken string
	expiresAt   time.Time
}

// Options — дополнительные настройки TLS.
type Options struct {
	CACertPath         string // путь к .pem сертификату Минцифры (опционально)
	InsecureSkipVerify bool   // отключить проверку сертификата (только для теста)
}

// New создаёт клиента GigaChat.
func New(authKey, scope, model string, opts Options) (*Client, error) {
	tlsConfig := &tls.Config{}

	if opts.InsecureSkipVerify {
		tlsConfig.InsecureSkipVerify = true
	} else if opts.CACertPath != "" {
		pool, err := loadCACert(opts.CACertPath)
		if err != nil {
			return nil, fmt.Errorf("не удалось загрузить сертификат из %s: %w", opts.CACertPath, err)
		}
		tlsConfig.RootCAs = pool
	}
	// Если ни то, ни другое не задано — используется системный пул доверенных
	// сертификатов. Запрос сработает, только если сертификат Минцифры уже
	// установлен в ОС.

	transport := &http.Transport{TLSClientConfig: tlsConfig}

	return &Client{
		authKey: authKey,
		scope:   scope,
		model:   model,
		httpClient: &http.Client{
			Timeout:   90 * time.Second,
			Transport: transport,
		},
	}, nil
}

func loadCACert(path string) (*x509.CertPool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(data) {
		return nil, fmt.Errorf("файл %s не похож на валидный PEM-сертификат", path)
	}
	return pool, nil
}

// ensureToken обновляет access_token, если он истёк или ещё не получен.
func (c *Client) ensureToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.accessToken != "" && time.Now().Before(c.expiresAt) {
		return c.accessToken, nil
	}

	form := bytes.NewBufferString("scope=" + c.scope)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, oauthURL, form)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("RqUID", uuid.NewString())
	req.Header.Set("Authorization", "Basic "+c.authKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("запрос токена: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gigachat oauth вернул статус %d: %s", resp.StatusCode, string(body))
	}

	var parsed struct {
		AccessToken string `json:"access_token"`
		ExpiresAt   int64  `json:"expires_at"` // unix ms
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("разбор ответа токена: %w, тело: %s", err, string(body))
	}

	c.accessToken = parsed.AccessToken
	// Оставляем запас в минуту, чтобы не словить протухший токен в середине запроса.
	c.expiresAt = time.UnixMilli(parsed.ExpiresAt).Add(-1 * time.Minute)

	return c.accessToken, nil
}

// uploadFile загружает изображение и возвращает его file_id.
func (c *Client) uploadFile(ctx context.Context, token string, photo []byte, mediaType string) (string, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	ext := ".jpg"
	if mediaType == "image/png" {
		ext = ".png"
	}
	header := make(textproto.MIMEHeader)
header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="photo%s"`, ext))
header.Set("Content-Type", mediaType)

part, err := writer.CreatePart(header)
if err != nil {
	return "", err
}
if _, err := part.Write(photo); err != nil {
	return "", err
}
	if err := writer.WriteField("purpose", "general"); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiBase+"/files", &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("загрузка файла: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gigachat /files вернул статус %d: %s", resp.StatusCode, string(body))
	}

	var parsed struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("разбор ответа /files: %w, тело: %s", err, string(body))
	}

	return parsed.ID, nil
}

type chatMessage struct {
	Role        string   `json:"role"`
	Content     string   `json:"content"`
	Attachments []string `json:"attachments,omitempty"`
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func (c *Client) chatCompletion(ctx context.Context, token string, messages []chatMessage) (string, error) {
	reqBody := chatRequest{
		Model:       c.model,
		Messages:    messages,
		Temperature: 0.3,
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiBase+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("chat completion: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gigachat chat/completions вернул статус %d: %s", resp.StatusCode, string(body))
	}

	var parsed chatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("разбор ответа chat/completions: %w, тело: %s", err, string(body))
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("пустой ответ от gigachat")
	}

	return parsed.Choices[0].Message.Content, nil
}

// AskWithPhoto отправляет вопрос вместе с фото. Системный промпт передаётся
// отдельным сообщением с ролью system, как принято в OpenAI-совместимых API.
func (c *Client) AskWithPhoto(ctx context.Context, systemPrompt, question string, photo []byte, mediaType string) (string, error) {
	token, err := c.ensureToken(ctx)
	if err != nil {
		return "", err
	}

	fileID, err := c.uploadFile(ctx, token, photo, mediaType)
	if err != nil {
		return "", err
	}

	messages := []chatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: question, Attachments: []string{fileID}},
	}

	return c.chatCompletion(ctx, token, messages)
}

// AskText отправляет обычный текстовый вопрос без фото.
func (c *Client) AskText(ctx context.Context, systemPrompt, question string) (string, error) {
	token, err := c.ensureToken(ctx)
	if err != nil {
		return "", err
	}

	messages := []chatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: question},
	}

	return c.chatCompletion(ctx, token, messages)
}
