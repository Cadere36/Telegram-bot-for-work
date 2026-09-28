// Package chunker режет текст документа на куски подходящего размера для
// эмбеддингов и последующего поиска.
package chunker

import "strings"

// Split разбивает текст на куски примерно по maxLen символов (рун, не байт —
// это важно для кириллицы, где одна буква занимает 2 байта в UTF-8), стараясь
// не разрывать абзацы (разделены \n). Если один абзац сам больше maxLen —
// он режется грубо по длине, но по границам символов, а не байт, чтобы не
// получить битую половину буквы на стыке кусков.
func Split(text string, maxLen int) []string {
	paragraphs := strings.Split(text, "\n")

	var chunks []string
	var current strings.Builder
	currentLen := 0 // длина текущего куска в рунах, не в байтах

	flush := func() {
		if currentLen > 0 {
			chunks = append(chunks, strings.TrimSpace(current.String()))
			current.Reset()
			currentLen = 0
		}
	}

	for _, p := range paragraphs {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		pRunes := []rune(p)

		// Если абзац сам по себе длиннее лимита — режем его отдельно кусками,
		// по рунам, чтобы не разорвать многобайтовый символ.
		if len(pRunes) > maxLen {
			flush()
			for len(pRunes) > maxLen {
				chunks = append(chunks, strings.TrimSpace(string(pRunes[:maxLen])))
				pRunes = pRunes[maxLen:]
			}
			if len(pRunes) > 0 {
				current.WriteString(string(pRunes))
				current.WriteString("\n")
				currentLen = len(pRunes) + 1
			}
			continue
		}

		if currentLen+len(pRunes)+1 > maxLen {
			flush()
		}
		current.WriteString(p)
		current.WriteString("\n")
		currentLen += len(pRunes) + 1
	}
	flush()

	return chunks
}
