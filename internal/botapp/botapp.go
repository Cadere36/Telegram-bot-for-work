// Package botapp связывает Telegram-бота, Claude API и (опционально) базу знаний
// в единый обработчик сообщений.
package botapp

import (
	"context"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"pb-bot/internal/llm"
	"pb-bot/internal/rag"
	"pb-bot/internal/store"
)

// systemPrompt задаёт роль модели и обязательную оговорку про ответственность.
// Это отдельная константа (а не часть пользовательского текста), чтобы коллеги
// не могли случайно или намеренно переопределить правила ответа своим сообщением.
const systemPromptTemplate = `Ты — ассистент по пожарной безопасности (ПБ) в организации.
Твоя задача — анализировать фото объектов и отвечать на вопросы коллег, опираясь
в первую очередь на приведённые ниже фрагменты нормативных документов — это
результат поиска по реальной базе нормативки организации.

Иерархия источников (соблюдай именно такой порядок при цитировании и выборе,
на что ссылаться в первую очередь):
1. Федеральные законы (ФЗ) — высший приоритет, это законодательный уровень
   требований (в первую очередь ФЗ-123 "Технический регламент о требованиях
   пожарной безопасности", но и другие, если они есть во фрагментах).
2. Своды правил (СП) и правила противопожарного режима (ППР РФ, постановления
   Правительства) — конкретизируют требования ФЗ, следующий уровень.
3. ГОСТ, ГОСТ Р (национальные и межгосударственные стандарты) — технические
   требования к конкретным изделиям и оборудованию.
4. Прочие документы (приказы МЧС, письма, судебная практика, решения ЕЭК и т.д.)
   — используются, если выше по иерархии ответа нет или он требует уточнения.
Если один и тот же вопрос закрывают документы разных уровней — упомяни оба, но
начни с более высокого уровня иерархии (сначала ФЗ, потом СП, потом ГОСТ).

Правила ответа:
1. Используй приведённые ниже фрагменты как основной источник фактов и точно
   указывай, из какого документа взят каждый пункт (название документа дано в
   квадратных скобках перед фрагментом, копируй его дословно, не сокращай и не
   заменяй на общие названия вроде "СП" или "ГОСТ" без номера, если в источнике
   указан конкретный документ).
2. Если вопрос касается фото — сначала опиши, что именно видно: какие объекты,
   их состояние, расположение, видимые детали.
3. Сопоставь увиденное или сказанное в вопросе с приведёнными фрагментами и
   укажи, есть ли нарушения, и с каким именно документом они связаны, соблюдая
   иерархию источников выше.
4. Для каждого найденного нарушения дай краткую рекомендацию по устранению:
   что конкретно нужно сделать, чтобы привести объект в соответствие (например,
   "установить дверцу шкафа с маркировкой ПК", "убрать посторонние предметы от
   пожарного крана", "заменить рукав, если истёк срок переукладки"). Рекомендации
   должны быть практическими и краткими, без общих фраз вроде "устранить нарушение".
5. Если приведённые фрагменты не отвечают на вопрос напрямую — честно скажи,
   что в найденной части базы точного ответа нет, и не подменяй это общими
   знаниями без явной оговорки, что это не из базы организации.
6. Обязательно заверши ответ фразой: "Это предварительная оценка, не акт
   проверки. Финальное заключение — за ответственным по ПБ."
7. ПИШИ ОБЫЧНЫМ ТЕКСТОМ БЕЗ MARKDOWN-РАЗМЕТКИ: никаких **, ##, таблиц через |,
   горизонтальных линий ---. Заголовки раздела — просто отдельной строкой с
   двоеточием. Списки — через обычный дефис "-" и перенос строки. Это сообщение
   в Telegram, разметка там не отображается и выглядит как мусор из символов.
8. Отвечай по-русски, кратко и по делу. Структурируй ответ разделами: "Что видно
   на фото", "Нарушения и нормы", "Рекомендации по устранению".

%s`

// descriptionSystemPrompt используется для отдельного, короткого запроса к
// модели — только чтобы понять, что изображено на фото, без анализа норм.
// Это описание потом используется как поисковый запрос к базе нормативки
// (сама фотография не участвует в векторном поиске, а дефолтный текст вопроса
// типа "проверь фото на соответствие" слишком общий и не находит нужные нормы).
const descriptionSystemPrompt = `Опиши кратко и только перечисли, какие объекты и
оборудование пожарной безопасности видны на фото: пожарный кран, пожарный шкаф,
огнетушитель, пожарная сигнализация, эвакуационный выход, путь эвакуации,
противопожарная дверь, электрощит, план эвакуации и т.п. Включи видимую
маркировку, надписи, состояние (открыт/закрыт, повреждён, заблокирован).
Не давай оценку соответствия нормам и не упоминай нарушения — только опиши,
что видно. Ответ — 2-4 короткие строки, без вступлений и выводов.`

// App — приложение бота.
type App struct {
	bot            *tgbotapi.BotAPI
	claude         llm.Client
	searcher       rag.Searcher
	store          *store.Store
	httpClient     *http.Client
	allowedUserIDs map[int64]bool // nil = доступ не ограничен (для обратной совместимости)
}


// New создаёт приложение бота. allowedUserIDsRaw — список Telegram user_id
// через запятую (из ALLOWED_TELEGRAM_USER_IDS), кому разрешено пользоваться
// ботом. Пустая строка означает, что ограничения нет (доступ у всех).
func New(bot *tgbotapi.BotAPI, claudeClient llm.Client, searcher rag.Searcher, st *store.Store, allowedUserIDsRaw string) *App {
	if searcher == nil {
		searcher = rag.NoopSearcher{}
	}

	allowed := parseAllowedUserIDs(allowedUserIDsRaw)
	if allowed == nil {
		log.Println("ВНИМАНИЕ: ALLOWED_TELEGRAM_USER_IDS не задан — доступ к боту НЕ ограничен, им может пользоваться кто угодно, кто найдёт бота")
	} else {
		log.Printf("доступ к боту ограничен списком из %d пользователей", len(allowed))
	}

	return &App{
		bot:            bot,
		claude:         claudeClient,
		searcher:       searcher,
		store:          st,
		httpClient:     &http.Client{Timeout: 30 * time.Second},
		allowedUserIDs: allowed,
	}
}

// parseAllowedUserIDs разбирает "123456,987654" в множество id. Пустая строка
// или отсутствие валидных чисел даёт nil — это значит "ограничения нет".
func parseAllowedUserIDs(raw string) map[int64]bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	set := make(map[int64]bool)
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			log.Printf("ALLOWED_TELEGRAM_USER_IDS: %q — не число, пропускаю", part)
			continue
		}
		set[id] = true
	}
	if len(set) == 0 {
		return nil
	}
	return set
}

// isAllowed проверяет, разрешён ли пользователю доступ к боту.
func (a *App) isAllowed(userID int64) bool {
	if a.allowedUserIDs == nil {
		return true
	}
	return a.allowedUserIDs[userID]
}

// Run запускает long-polling и обрабатывает входящие сообщения.
func (a *App) Run() {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := a.bot.GetUpdatesChan(u)

	log.Println("бот запущен, ждём сообщения...")

	for update := range updates {
		if update.Message == nil {
			continue
		}
		go a.handleMessage(update.Message)
	}
}

func (a *App) handleMessage(msg *tgbotapi.Message) {
	if msg.From == nil || !a.isAllowed(msg.From.ID) {
		var uid int64
		username := "неизвестно"
		if msg.From != nil {
			uid = msg.From.ID
			username = msg.From.UserName
		}
		log.Printf("доступ запрещён: user_id=%d username=%q", uid, username)
		a.reply(msg.Chat.ID, "У вас нет доступа к этому боту. Обратитесь к ответственному по пожарной безопасности, чтобы вас добавили в список разрешённых пользователей.")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	

	question := strings.TrimSpace(msg.Caption)
	if question == "" {
		question = strings.TrimSpace(msg.Text)
	}
	if question == "" {
		question = "Проверь фото на соответствие требованиям пожарной безопасности и укажи возможные нарушения."
	}

	a.sendTyping(msg.Chat.ID)

	var answer string
	var err error
	hasPhoto := len(msg.Photo) > 0

	if hasPhoto {
		answer, err = a.handlePhoto(ctx, msg, question)
	} else {
		answer, err = a.handleText(ctx, question)
	}

	if err != nil {
		log.Printf("ошибка обработки сообщения от %d: %v", msg.From.ID, err)
		a.reply(msg.Chat.ID, "Не получилось обработать запрос: "+err.Error())
		return
	}

	a.reply(msg.Chat.ID, answer)

	if a.store != nil {
		logErr := a.store.LogInteraction(ctx, store.LogEntry{
			TelegramUserID: msg.From.ID,
			Username:       msg.From.UserName,
			Question:       question,
			HasPhoto:       hasPhoto,
			Answer:         answer,
		})
		if logErr != nil {
			log.Printf("не удалось записать лог: %v", logErr)
		}
	}
}

func (a *App) handlePhoto(ctx context.Context, msg *tgbotapi.Message, question string) (string, error) {
	// Берём фото наибольшего размера (последний элемент среза).
	photoSize := msg.Photo[len(msg.Photo)-1]

	fileURL, err := a.bot.GetFileDirectURL(photoSize.FileID)
	if err != nil {
		return "", err
	}

	photoBytes, mediaType, err := downloadFile(ctx, a.httpClient, fileURL)
	if err != nil {
		return "", err
	}

	// Шаг 1: отдельным быстрым запросом узнаём, что вообще изображено на фото.
	// Дефолтный текст вопроса ("проверь фото на соответствие...") слишком общий
	// и сам по себе не находит нужные нормы при векторном поиске — нужно
	// сначала получить конкретные термины (пожарный кран, шкаф и т.п.).
	description, descErr := a.claude.AskWithPhoto(ctx, descriptionSystemPrompt, "Опиши, что видно на фото.", photoBytes, mediaType)
	if descErr != nil {
		log.Printf("не удалось получить описание фото для RAG-поиска (продолжаем по обычному вопросу): %v", descErr)
		description = ""
	}
	log.Printf("DEBUG: описание фото для поиска: %q", description)

	// Шаг 2: ищем нормативку по описанию + исходному вопросу (если коллега
	// что-то написал в подписи — это тоже важно для поиска).
	searchQuery := question
	if description != "" {
		searchQuery = description + "\n" + question
	}

	chunks, err := a.searcher.Search(ctx, searchQuery, 12)
	log.Printf("DEBUG: найдено кусков RAG: %d (запрос: %q)", len(chunks), searchQuery)
	for _, c := range chunks {
		log.Printf("DEBUG: источник=%q, текст=%.100s...", c.Source, c.Text)
	}
	if err != nil {
		log.Printf("ошибка поиска по базе нормативки (продолжаем без неё): %v", err)
	}
	systemPrompt := buildSystemPrompt(chunks)

	// Шаг 3: финальный ответ — с фото, исходным вопросом и найденным контекстом.
	return a.claude.AskWithPhoto(ctx, systemPrompt, question, photoBytes, mediaType)
}

func (a *App) handleText(ctx context.Context, question string) (string, error) {
	chunks, err := a.searcher.Search(ctx, question, 5)
	if err != nil {
		log.Printf("ошибка поиска по базе нормативки (продолжаем без неё): %v", err)
	}
	systemPrompt := buildSystemPrompt(chunks)

	return a.claude.AskText(ctx, systemPrompt, question)
}

func buildSystemPrompt(chunks []rag.Chunk) string {
	context := rag.BuildContext(chunks)
	if context == "" {
		context = "(по этому вопросу в базе нормативки не нашлось релевантных фрагментов — отвечай на основе общих знаний о требованиях ПБ и явно скажи, что это не проверка по конкретным документам организации)"
	}
	return sprintfSystemPrompt(context)
}

func sprintfSystemPrompt(context string) string {
	return replaceOnce(systemPromptTemplate, "%s", context)
}

// replaceOnce — маленький хелпер вместо fmt.Sprintf, чтобы не путаться со
// знаками процента, которые могут встретиться в тексте норм.
func replaceOnce(template, placeholder, value string) string {
	idx := strings.Index(template, placeholder)
	if idx == -1 {
		return template
	}
	return template[:idx] + value + template[idx+len(placeholder):]
}

func (a *App) sendTyping(chatID int64) {
	action := tgbotapi.NewChatAction(chatID, tgbotapi.ChatTyping)
	_, _ = a.bot.Send(action)
}

func (a *App) reply(chatID int64, text string) {
	// Telegram ограничивает сообщение 4096 символами — режем на части.
	// Резать нужно по рунам, а не по байтам: кириллица занимает 2 байта на
	// символ в UTF-8, и обрезка по байтовому индексу может разорвать символ
	// пополам — тогда Telegram отклоняет сообщение как невалидный UTF-8.
	const maxLen = 4000
	runes := []rune(text)
	for len(runes) > 0 {
		end := maxLen
		if end > len(runes) {
			end = len(runes)
		}
		chunk := string(runes[:end])
		msg := tgbotapi.NewMessage(chatID, chunk)
		if _, err := a.bot.Send(msg); err != nil {
			log.Printf("не удалось отправить сообщение: %v", err)
			return
		}
		runes = runes[end:]
	}
}

func downloadFile(ctx context.Context, client *http.Client, url string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}

	mediaType := resp.Header.Get("Content-Type")
	if mediaType == "" || mediaType == "application/octet-stream" {
		// Telegram-фото почти всегда JPEG.
		mediaType = "image/jpeg"
	}

	return data, mediaType, nil
}
