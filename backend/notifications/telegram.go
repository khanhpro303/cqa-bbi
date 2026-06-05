package notifications

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/vietbui/chat-quality-agent/channels"
)

const telegramAPIBase = "https://api.telegram.org/bot%s/%s"

// telegramMaxRunes is a conservative per-message cap. Telegram rejects messages
// longer than 4096 UTF-16 code units; counting runes with margin keeps every
// chunk safely under that limit even when the text carries emoji or CJK, and
// leaves room for the "(i/n)" part counter appended to multi-part alerts.
const telegramMaxRunes = 3500

type TelegramNotifier struct {
	botToken string
	chatID   string
	client   *http.Client
}

func NewTelegramNotifier(botToken, chatID string) *TelegramNotifier {
	return &TelegramNotifier{
		botToken: botToken,
		chatID:   chatID,
		client:   &http.Client{Timeout: 30 * time.Second},
	}
}

func (t *TelegramNotifier) Send(ctx context.Context, subject string, body string) error {
	// Split over-long alerts (e.g. a campaign failure list spanning many groups)
	// into ordered parts instead of byte-truncating, which previously dropped the
	// tail and could cut a multibyte rune mid-character.
	chunks := buildTelegramChunks(subject, body)
	for i, chunk := range chunks {
		if err := t.sendMessage(ctx, chunk); err != nil {
			if len(chunks) > 1 {
				return fmt.Errorf("telegram send part %d/%d failed: %w", i+1, len(chunks), err)
			}
			return err
		}
	}
	return nil
}

// buildTelegramChunks combines subject + body (HTML), then splits the result into
// rune-safe, newline-preferring pieces. When more than one piece is produced each
// is prefixed with an "(i/n)" counter so the reader can see the order. Pure (no
// I/O) so it is unit-testable.
func buildTelegramChunks(subject, body string) []string {
	text := body
	if subject != "" {
		text = fmt.Sprintf("<b>%s</b>\n\n%s", subject, body)
	}

	chunks := channels.ChunkText(text, telegramMaxRunes)
	if len(chunks) <= 1 {
		return chunks
	}
	for i := range chunks {
		chunks[i] = fmt.Sprintf("(%d/%d)\n%s", i+1, len(chunks), chunks[i])
	}
	return chunks
}

// sendMessage posts a single prepared message to Telegram and surfaces an API
// error (ok=false) as a real error.
func (t *TelegramNotifier) sendMessage(ctx context.Context, text string) error {
	payload := map[string]interface{}{
		"chat_id":    t.chatID,
		"text":       text,
		"parse_mode": "HTML",
	}

	payloadBytes, _ := json.Marshal(payload)
	url := fmt.Sprintf(telegramAPIBase, t.botToken, "sendMessage")

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(payloadBytes))
	if err != nil {
		return fmt.Errorf("create telegram request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("telegram send failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read telegram response: %w", err)
	}

	var result struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("parse telegram response: %w", err)
	}

	if !result.OK {
		return fmt.Errorf("telegram api error: %s", result.Description)
	}

	return nil
}

func (t *TelegramNotifier) HealthCheck(ctx context.Context) error {
	url := fmt.Sprintf(telegramAPIBase, t.botToken, "getMe")
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("create telegram health check request: %w", err)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("telegram health check failed: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		OK bool `json:"ok"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	if !result.OK {
		return fmt.Errorf("telegram bot not accessible")
	}
	return nil
}
