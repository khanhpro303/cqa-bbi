package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const fbGraphBase = "https://graph.facebook.com/v21.0"

// FacebookCredentials holds credentials for Facebook Graph API.
type FacebookCredentials struct {
	PageID      string `json:"page_id"`
	AccessToken string `json:"access_token"` // Long-lived page access token
}

type FacebookAdapter struct {
	creds  FacebookCredentials
	client *http.Client
}

func NewFacebookAdapter(creds FacebookCredentials) *FacebookAdapter {
	return &FacebookAdapter{
		creds:  creds,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (f *FacebookAdapter) doRequest(ctx context.Context, url string) (map[string]interface{}, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create facebook api request: %w", err)
	}

	// Add access_token if not already in URL
	q := req.URL.Query()
	if q.Get("access_token") == "" {
		q.Set("access_token", f.creds.AccessToken)
		req.URL.RawQuery = q.Encode()
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("facebook api request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("facebook api read body failed: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("facebook api decode failed: %w", err)
	}

	if errObj, ok := result["error"].(map[string]interface{}); ok {
		msg, _ := errObj["message"].(string)
		code, _ := errObj["code"].(float64)
		return nil, fmt.Errorf("facebook api error: (#%.0f) %s", code, msg)
	}

	return result, nil
}

func (f *FacebookAdapter) FetchRecentConversations(ctx context.Context, since time.Time, limit int) ([]SyncedConversation, error) {
	var conversations []SyncedConversation
	nextURL := fmt.Sprintf("%s/%s/conversations?fields=id,link,updated_time,participants&limit=100",
		fbGraphBase, f.creds.PageID)

	for nextURL != "" {
		if limit > 0 && len(conversations) >= limit {
			break
		}

		result, err := f.doRequest(ctx, nextURL)
		if err != nil {
			return conversations, err
		}

		data, ok := result["data"].([]interface{})
		if !ok || len(data) == 0 {
			break
		}

		for _, item := range data {
			conv, ok := item.(map[string]interface{})
			if !ok {
				continue
			}

			convID, _ := conv["id"].(string)

			var updatedAt time.Time
			if updStr, ok := conv["updated_time"].(string); ok {
				updatedAt, _ = time.Parse("2006-01-02T15:04:05-0700", updStr)
			}

			if !since.IsZero() && updatedAt.Before(since) {
				return conversations, nil // FB returns sorted by updated_time desc
			}

			// Extract participant name (the non-page user)
			customerName := ""
			if participants, ok := conv["participants"].(map[string]interface{}); ok {
				if pData, ok := participants["data"].([]interface{}); ok {
					for _, p := range pData {
						participant, _ := p.(map[string]interface{})
						pID, _ := participant["id"].(string)
						if pID != f.creds.PageID {
							customerName, _ = participant["name"].(string)
							break
						}
					}
				}
			}

			conversations = append(conversations, SyncedConversation{
				ExternalID:     convID,
				ExternalUserID: convID,
				CustomerName:   customerName,
				LastMessageAt:  updatedAt,
				Metadata:       conv,
			})
		}

		// Cursor-based pagination
		nextURL = ""
		if paging, ok := result["paging"].(map[string]interface{}); ok {
			if next, ok := paging["next"].(string); ok {
				nextURL = next
			}
		}
	}

	return conversations, nil
}

func (f *FacebookAdapter) FetchMessages(ctx context.Context, conversationID string, since time.Time) ([]SyncedMessage, error) {
	var messages []SyncedMessage
	nextURL := fmt.Sprintf("%s/%s/messages?fields=id,message,from,to,created_time,attachments,shares,sticker&limit=100",
		fbGraphBase, conversationID)

	for nextURL != "" {
		result, err := f.doRequest(ctx, nextURL)
		if err != nil {
			return messages, err
		}

		data, ok := result["data"].([]interface{})
		if !ok || len(data) == 0 {
			break
		}

		for _, item := range data {
			msg, ok := item.(map[string]interface{})
			if !ok {
				continue
			}

			var sentAt time.Time
			if ts, ok := msg["created_time"].(string); ok {
				sentAt, _ = time.Parse("2006-01-02T15:04:05-0700", ts)
			}

			if !since.IsZero() && sentAt.Before(since) {
				return messages, nil
			}

			msgID, _ := msg["id"].(string)
			content, _ := msg["message"].(string)

			// Determine sender type
			senderType := "customer"
			senderName := ""
			if from, ok := msg["from"].(map[string]interface{}); ok {
				fromID, _ := from["id"].(string)
				senderName, _ = from["name"].(string)
				if fromID == f.creds.PageID {
					senderType = "agent"
				}
			}

			syncedMsg := SyncedMessage{
				ExternalID:  msgID,
				SenderType:  senderType,
				SenderName:  senderName,
				Content:     content,
				ContentType: "text",
				SentAt:      sentAt,
				RawData:     msg,
			}

			// Parse attachments
			if attachData, ok := msg["attachments"].(map[string]interface{}); ok {
				if aData, ok := attachData["data"].([]interface{}); ok {
					for _, a := range aData {
						att, _ := a.(map[string]interface{})
						aType, _ := att["mime_type"].(string)
						aName, _ := att["name"].(string)
						aURL := ""
						if payload, ok := att["image_data"].(map[string]interface{}); ok {
							aURL, _ = payload["url"].(string)
						} else if payload, ok := att["video_data"].(map[string]interface{}); ok {
							aURL, _ = payload["url"].(string)
						} else if fileURL, ok := att["file_url"].(string); ok {
							aURL = fileURL
						}
						// Fallback: top-level url field
						if aURL == "" {
							if topURL, ok := att["url"].(string); ok {
								aURL = topURL
							}
						}
						// Fallback: media.image.src (StoryAttachment format)
						if aURL == "" {
							if media, ok := att["media"].(map[string]interface{}); ok {
								if img, ok := media["image"].(map[string]interface{}); ok {
									aURL, _ = img["src"].(string)
								}
							}
						}
						syncedMsg.Attachments = append(syncedMsg.Attachments, Attachment{
							Type: aType,
							URL:  aURL,
							Name: aName,
						})
					}
					if len(syncedMsg.Attachments) > 0 {
						syncedMsg.ContentType = "attachment"
					}
				}
			}

			// Sticker
			if _, ok := msg["sticker"].(string); ok {
				syncedMsg.ContentType = "sticker"
			}

			messages = append(messages, syncedMsg)
		}

		// Cursor pagination
		nextURL = ""
		if paging, ok := result["paging"].(map[string]interface{}); ok {
			if next, ok := paging["next"].(string); ok {
				nextURL = next
			}
		}
	}

	return messages, nil
}

func (f *FacebookAdapter) HealthCheck(ctx context.Context) error {
	url := fmt.Sprintf("%s/%s?fields=id,name", fbGraphBase, f.creds.PageID)
	_, err := f.doRequest(ctx, url)
	return err
}

func (f *FacebookAdapter) SendMessage(ctx context.Context, conversationID string, content string) error {
	// TODO: implement facebook send message API
	return fmt.Errorf("facebook send message not implemented yet")
}

