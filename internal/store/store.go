// Package store отвечает за хранение логов обращений и базы нормативных
// документов (для RAG-поиска) в Postgres с расширением pgvector.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

// Store — обёртка над подключением к Postgres.
type Store struct {
	db *sql.DB
}

// Open открывает соединение и проверяет его пингом.
func Open(databaseURL string) (*Store, error) {
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

// Close закрывает соединение с БД.
func (s *Store) Close() error {
	return s.db.Close()
}

// Migrate прогоняет минимальные миграции (создание таблиц), если их ещё нет.
// Для MVP этого достаточно; при росте проекта стоит перейти на нормальный
// инструмент миграций (goose, migrate).
func (s *Store) Migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, schemaSQL)
	return err
}

// LogEntry — одна запись в журнале обращений.
type LogEntry struct {
	TelegramUserID int64
	Username       string
	Question       string
	HasPhoto       bool
	Answer         string
}

// LogInteraction сохраняет вопрос пользователя и ответ модели.
func (s *Store) LogInteraction(ctx context.Context, e LogEntry) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO interaction_log (telegram_user_id, username, question, has_photo, answer, created_at)
		VALUES ($1, $2, $3, $4, $5, now())
	`, e.TelegramUserID, e.Username, e.Question, e.HasPhoto, e.Answer)
	return err
}

// EMBEDDING_DIMENSION должен совпадать с размерностью модели эмбеддингов
// (у qwen/qwen3-embedding-8b это 4096 — см. internal/polza/embeddings.go).
const embeddingDimension = 4096

const schemaSQL = `
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS interaction_log (
    id               BIGSERIAL PRIMARY KEY,
    telegram_user_id BIGINT NOT NULL,
    username         TEXT,
    question         TEXT,
    has_photo        BOOLEAN NOT NULL DEFAULT false,
    answer           TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS documents (
    id         BIGSERIAL PRIMARY KEY,
    title      TEXT NOT NULL,
    source     TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS document_chunks (
    id          BIGSERIAL PRIMARY KEY,
    document_id BIGINT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    chunk_text  TEXT NOT NULL,
    embedding   vector(4096)
);
`

// InsertDocument добавляет документ (например, один файл .docx) и возвращает его id.
// Если документ с таким title уже есть — он и его чанки удаляются и создаются заново
// (удобно для повторного запуска индексатора после правки базы нормативки).
func (s *Store) InsertDocument(ctx context.Context, title, source string) (int64, error) {
	_, err := s.db.ExecContext(ctx, `DELETE FROM documents WHERE title = $1`, title)
	if err != nil {
		return 0, fmt.Errorf("удаление старой версии документа: %w", err)
	}

	var id int64
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO documents (title, source) VALUES ($1, $2) RETURNING id
	`, title, source).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("вставка документа: %w", err)
	}
	return id, nil
}

// InsertChunk сохраняет один кусок текста документа вместе с его вектором.
func (s *Store) InsertChunk(ctx context.Context, documentID int64, text string, embedding []float32) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO document_chunks (document_id, chunk_text, embedding)
		VALUES ($1, $2, $3::vector)
	`, documentID, text, vectorLiteral(embedding))
	return err
}

// ChunkResult — один найденный кусок документа с указанием источника.
type ChunkResult struct {
	Source string
	Text   string
}

// SearchChunks ищет topK кусков документов, ближайших по смыслу к переданному
// вектору вопроса (косинусное расстояние — оператор <=> из pgvector).
// Чтобы один документ (например, ГОСТ с повторяющимся юридическим бойлерплейтом
// про "применение на добровольной основе") не занимал все top-K слотов и не
// вытеснял другие релевантные источники, на документ берётся не больше
// maxPerDocument кусков.
func (s *Store) SearchChunks(ctx context.Context, queryEmbedding []float32, topK int) ([]ChunkResult, error) {
	const maxPerDocument = 2

	rows, err := s.db.QueryContext(ctx, `
		WITH ranked AS (
			SELECT
				d.title,
				c.chunk_text,
				c.embedding <=> $1::vector AS distance,
				ROW_NUMBER() OVER (
					PARTITION BY c.document_id
					ORDER BY c.embedding <=> $1::vector
				) AS rn
			FROM document_chunks c
			JOIN documents d ON d.id = c.document_id
		)
		SELECT title, chunk_text
		FROM ranked
		WHERE rn <= $3
		ORDER BY distance
		LIMIT $2
	`, vectorLiteral(queryEmbedding), topK, maxPerDocument)
	if err != nil {
		return nil, fmt.Errorf("поиск по векторам: %w", err)
	}
	defer rows.Close()

	var results []ChunkResult
	for rows.Next() {
		var r ChunkResult
		if err := rows.Scan(&r.Source, &r.Text); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// vectorLiteral форматирует вектор в текстовый вид, который pgvector понимает
// после приведения типа ::vector, например "[0.01,-0.02,0.03]".
func vectorLiteral(v []float32) string {
	parts := make([]string, len(v))
	for i, f := range v {
		parts[i] = strconv.FormatFloat(float64(f), 'f', 8, 32)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

var _ = embeddingDimension // используется только как документация размерности выше
