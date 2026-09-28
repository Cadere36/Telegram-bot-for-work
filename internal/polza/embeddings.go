package polza

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const embeddingsURL = "https://polza.ai/api/v1/embeddings"

// EmbeddingModel — модель эмбеддингов, рекомендованная Polza для русского текста.
const EmbeddingModel = "qwen/qwen3-embedding-8b"

// EmbeddingDimension — размерность вектора у qwen3-embedding-8b.
const EmbeddingDimension = 4096

type embeddingsRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embeddingsResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Embed превращает список текстов в список векторов (в том же порядке).
// За один вызов лучше передавать не больше 20-30 текстов, чтобы запрос
// не был слишком большим и не упирался в лимиты API.
func (c *Client) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	reqBody := embeddingsRequest{
		Model: EmbeddingModel,
		Input: texts,
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal embeddings request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, embeddingsURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var parsed embeddingsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("unmarshal embeddings response (status %d): %w, body: %s", resp.StatusCode, err, string(body))
	}
	if parsed.Error != nil {
		return nil, fmt.Errorf("polza embeddings error: %s", parsed.Error.Message)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("polza embeddings вернул статус %d: %s", resp.StatusCode, string(body))
	}

	result := make([][]float32, len(parsed.Data))
	for _, d := range parsed.Data {
		if d.Index < 0 || d.Index >= len(result) {
			continue
		}
		result[d.Index] = d.Embedding
	}
	return result, nil
}
