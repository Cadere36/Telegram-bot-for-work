// Команда debugsearch — диагностика векторного поиска: эмбеддит тестовый
// запрос (с инструктивным префиксом и без) и печатает топ-N ближайших
// кусков с их косинусным расстоянием, чтобы понять, где проблема.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/joho/godotenv"

	"pb-bot/internal/polza"
	"pb-bot/internal/store"
)

func main() {
	query := flag.String("q", "пожарный кран пожарный шкаф размещение и требования", "текст запроса")
	topK := flag.Int("k", 15, "сколько результатов показать")
	flag.Parse()

	_ = godotenv.Load()
	databaseURL := os.Getenv("DATABASE_URL")
	polzaKey := os.Getenv("POLZA_API_KEY")
	if databaseURL == "" || polzaKey == "" {
		log.Fatal("нужны DATABASE_URL и POLZA_API_KEY в .env")
	}

	st, err := store.Open(databaseURL)
	if err != nil {
		log.Fatalf("БД: %v", err)
	}
	defer st.Close()

	embedder := polza.New(polzaKey, "")
	ctx := context.Background()

	run := func(label, text string) {
		fmt.Printf("\n=== %s ===\nТекст запроса для эмбеддинга: %q\n", label, text)
		vecs, err := embedder.Embed(ctx, []string{text})
		if err != nil {
			log.Fatalf("эмбеддинг: %v", err)
		}
		results, err := st.SearchChunks(ctx, vecs[0], *topK)
		if err != nil {
			log.Fatalf("поиск: %v", err)
		}
		for i, r := range results {
			preview := r.Text
			if len(preview) > 90 {
				preview = preview[:90]
			}
			fmt.Printf("%2d. [%s] %s...\n", i+1, r.Source, preview)
		}
	}

	// Вариант A: как сейчас в проде — голый текст без инструкции.
	run("БЕЗ префикса (как сейчас)", *query)

	// Вариант B: с инструктивным префиксом, как рекомендует Qwen3-Embedding для запросов.
	instructed := fmt.Sprintf("Instruct: Given a question, retrieve relevant passages that answer the question\nQuery: %s", *query)
	run("С префиксом Instruct/Query", instructed)
}