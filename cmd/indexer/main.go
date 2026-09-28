// Команда indexer читает все .docx из указанной папки, режет их на куски,
// получает эмбеддинги через Polza и складывает всё в Postgres (pgvector).
// Запускается один раз при первой загрузке базы нормативки и повторно —
// при обновлении документов (старая версия документа с тем же именем
// удаляется и создаётся заново, см. store.InsertDocument).
//
// Использование:
//
//	go run ./cmd/indexer -dir /путь/к/папке/с/docx
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"pb-bot/internal/chunker"
	"pb-bot/internal/docxtext"
	"pb-bot/internal/polza"
	"pb-bot/internal/store"
)

// chunkMaxLen — примерный максимальный размер одного куска текста в символах.
const chunkMaxLen = 1000

// embedBatchSize — сколько кусков отправлять на эмбеддинг за один запрос к API.
const embedBatchSize = 10

func main() {
	dir := flag.String("dir", "", "папка с .docx файлами нормативки")
	flag.Parse()

	if *dir == "" {
		log.Fatal("укажите папку с документами: go run ./cmd/indexer -dir /путь/к/папке")
	}

	_ = godotenv.Load()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("не задан DATABASE_URL в .env")
	}
	polzaKey := os.Getenv("POLZA_API_KEY")
	if polzaKey == "" {
		log.Fatal("не задан POLZA_API_KEY в .env")
	}

	st, err := store.Open(databaseURL)
	if err != nil {
		log.Fatalf("не удалось подключиться к БД: %v", err)
	}
	defer st.Close()

	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		log.Fatalf("ошибка миграции БД: %v", err)
	}

	embedder := polza.New(polzaKey, "" /* модель для чата тут не нужна */)

	files, err := findDocxFiles(*dir)
	if err != nil {
		log.Fatalf("не удалось прочитать папку: %v", err)
	}
	log.Printf("найдено .docx файлов: %d", len(files))

	var totalChunks int
	var failedFiles []string

	for i, path := range files {
		title := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		log.Printf("[%d/%d] %s", i+1, len(files), title)

		if err := indexFile(ctx, st, embedder, path, title); err != nil {
			log.Printf("  ОШИБКА: %v", err)
			failedFiles = append(failedFiles, title)
			continue
		}
	}

	log.Printf("готово. Обработано файлов: %d, ошибок: %d, куски: %d", len(files)-len(failedFiles), len(failedFiles), totalChunks)
	if len(failedFiles) > 0 {
		log.Printf("файлы с ошибками (проверьте вручную): %s", strings.Join(failedFiles, "; "))
	}
}

func findDocxFiles(dir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(path), ".docx") {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

func indexFile(ctx context.Context, st *store.Store, embedder *polza.Client, path, title string) error {
	text, err := docxtext.Extract(path)
	if err != nil {
		return fmt.Errorf("извлечение текста: %w", err)
	}
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("документ пустой после извлечения текста")
	}

	chunks := chunker.Split(text, chunkMaxLen)
	if len(chunks) == 0 {
		return fmt.Errorf("не удалось разбить документ на куски")
	}

	documentID, err := st.InsertDocument(ctx, title, path)
	if err != nil {
		return fmt.Errorf("вставка документа: %w", err)
	}

	for start := 0; start < len(chunks); start += embedBatchSize {
		end := start + embedBatchSize
		if end > len(chunks) {
			end = len(chunks)
		}
		batch := chunks[start:end]

		embedCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		vectors, err := embedder.Embed(embedCtx, batch)
		cancel()
		if err != nil {
			return fmt.Errorf("эмбеддинг кусков %d-%d: %w", start, end, err)
		}

		for i, vec := range vectors {
			if vec == nil {
				continue
			}
			if err := st.InsertChunk(ctx, documentID, batch[i], vec); err != nil {
				return fmt.Errorf("сохранение куска: %w", err)
			}
		}
	}

	log.Printf("  сохранено кусков: %d", len(chunks))
	return nil
}
