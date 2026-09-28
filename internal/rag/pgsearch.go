package rag

import (
	"context"
	"fmt"

	"pb-bot/internal/store"
)

// Embedder — то, что умеет превращать тексты в векторы (реализовано в
// internal/polza через Embed).
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// PgSearcher ищет релевантные фрагменты нормативки в Postgres (pgvector).
type PgSearcher struct {
	store    *store.Store
	embedder Embedder
}

// NewPgSearcher создаёт поисковик поверх БД и провайдера эмбеддингов.
func NewPgSearcher(st *store.Store, embedder Embedder) *PgSearcher {
	return &PgSearcher{store: st, embedder: embedder}
}

// Search реализует интерфейс Searcher: превращает вопрос в вектор и находит
// topK ближайших по смыслу кусков документов.
func (p *PgSearcher) Search(ctx context.Context, query string, topK int) ([]Chunk, error) {
	vectors, err := p.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("эмбеддинг вопроса: %w", err)
	}
	if len(vectors) == 0 || vectors[0] == nil {
		return nil, fmt.Errorf("пустой эмбеддинг вопроса")
	}

	results, err := p.store.SearchChunks(ctx, vectors[0], topK)
	if err != nil {
		return nil, err
	}

	chunks := make([]Chunk, len(results))
	for i, r := range results {
		chunks[i] = Chunk{Source: r.Source, Text: r.Text}
	}
	return chunks, nil
}
