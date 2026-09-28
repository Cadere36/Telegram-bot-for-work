// Package rag — заготовка под поиск релевантных фрагментов нормативки (RAG).
// На шаге 1 не используется: Search возвращает пустой контекст, и модель отвечает
// на основе общих знаний + фото. На шаге 2, когда документы будут загружены и
// проиндексированы в pgvector, здесь появится реальный поиск по эмбеддингам.
package rag

import "context"

// Chunk — один найденный фрагмент нормативного документа.
type Chunk struct {
	Source string // например "СП 3.13130.2009, п. 4.1.5"
	Text   string
}

// Searcher ищет релевантные фрагменты нормативки по вопросу пользователя.
type Searcher interface {
	Search(ctx context.Context, query string, topK int) ([]Chunk, error)
}

// NoopSearcher — заглушка для шага 1: база знаний ещё не подключена.
type NoopSearcher struct{}

func (NoopSearcher) Search(ctx context.Context, query string, topK int) ([]Chunk, error) {
	return nil, nil
}

// BuildContext собирает найденные фрагменты в текст для системного промпта.
func BuildContext(chunks []Chunk) string {
	if len(chunks) == 0 {
		return ""
	}
	out := "Релевантные фрагменты нормативных документов:\n\n"
	for _, c := range chunks {
		out += "— [" + c.Source + "]: " + c.Text + "\n\n"
	}
	return out
}
