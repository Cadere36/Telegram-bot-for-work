// Package docxtext извлекает обычный текст из файлов .docx без сторонних
// библиотек — .docx это zip-архив с XML внутри, а нам нужен только текст,
// поэтому используем стандартные archive/zip и encoding/xml.
package docxtext

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// Extract открывает .docx по пути и возвращает его текстовое содержимое.
// Абзацы разделяются символом новой строки.
func Extract(path string) (string, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("открытие .docx как zip: %w", err)
	}
	defer r.Close()

	var docFile *zip.File
	for _, f := range r.File {
		if f.Name == "word/document.xml" {
			docFile = f
			break
		}
	}
	if docFile == nil {
		return "", fmt.Errorf("в архиве не найден word/document.xml — это не .docx или файл повреждён")
	}

	rc, err := docFile.Open()
	if err != nil {
		return "", fmt.Errorf("чтение word/document.xml: %w", err)
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		return "", err
	}

	return extractTextFromXML(data)
}

// extractTextFromXML идёт по XML-токенам и собирает текст из элементов <w:t>,
// вставляя перевод строки на закрытии каждого абзаца <w:p>.
func extractTextFromXML(data []byte) (string, error) {
	decoder := xml.NewDecoder(strings.NewReader(string(data)))

	var sb strings.Builder
	inText := false

	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("разбор XML: %w", err)
		}

		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "t" {
				inText = true
			}
			// <w:tab/> и <w:br/> в реальном документе означают пробел/перенос —
			// добавляем пробел, чтобы слова не склеивались.
			if t.Name.Local == "tab" || t.Name.Local == "br" || t.Name.Local == "cr" {
				sb.WriteString(" ")
			}
		case xml.EndElement:
			if t.Name.Local == "t" {
				inText = false
			}
			if t.Name.Local == "p" {
				sb.WriteString("\n")
			}
		case xml.CharData:
			if inText {
				sb.Write(t)
			}
		}
	}

	return sb.String(), nil
}
