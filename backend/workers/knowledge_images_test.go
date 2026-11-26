package workers

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestSendKnowledgeImageRechecksVisibilityAfterUpload(t *testing.T) {
	const imageID = "cea6c2f1-40bc-4c42-a256-e2f676d24e9d"
	var picture bytes.Buffer
	if err := png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 5, 7))); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, groupID string
		revoke        bool
	}{
		{name: "direct"},
		{name: "group", groupID: "group-id"},
		{name: "disabled while uploading", revoke: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			database, err := gorm.Open(mysql.New(mysql.Config{
				DSN: "fixture@tcp(127.0.0.1:3306)/fixture", SkipInitializeWithVersion: true,
			}), &gorm.Config{DryRun: true, DisableAutomaticPing: true})
			if err != nil {
				t.Fatal(err)
			}
			previousDB, previousTransport := db.DB, http.DefaultTransport
			db.DB = database
			t.Cleanup(func() {
				db.DB, http.DefaultTransport = previousDB, previousTransport
				pool, _ := database.DB()
				_ = pool.Close()
			})

			queries, uploads, sends := 0, 0, 0
			if err := database.Callback().Query().After("gorm:query").Register("knowledge-image-fixture", func(q *gorm.DB) {
				queries++
				if !strings.Contains(q.Statement.SQL.String(), "d.audience = ?") {
					t.Error("public OA image lookup lacks audience check")
				}
				rows := q.Statement.Dest.(*[]models.KnowledgeDocumentImage)
				if queries == 1 || !tc.revoke {
					*rows = []models.KnowledgeDocumentImage{{ID: imageID, Data: picture.Bytes()}}
				}
			}); err != nil {
				t.Fatal(err)
			}
			http.DefaultTransport = stockTestTransport(func(r *http.Request) (*http.Response, error) {
				body := `{"error":0,"data":{"attachment_id":"uploaded"}}`
				if strings.HasSuffix(r.URL.Path, "/upload/image") {
					uploads++
				} else {
					sends++
					var message struct {
						Recipient map[string]string `json:"recipient"`
					}
					if err := json.NewDecoder(r.Body).Decode(&message); err != nil {
						t.Error(err)
					}
					if tc.groupID == "" && message.Recipient["user_id"] != "user-id" ||
						tc.groupID != "" && message.Recipient["group_id"] != tc.groupID {
						t.Errorf("unexpected image recipient: %v", message.Recipient)
					}
					body = `{"error":0,"data":{"group_id":"group-id","message_id":"sent"}}`
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			adapter := channels.NewZaloOAAdapter(channels.ZaloOACredentials{})
			attempted, err := sendKnowledgeImage(context.Background(), adapter, "tenant", "public", "user-id", tc.groupID, []string{imageID})
			if err != nil {
				t.Fatal(err)
			}
			if queries != 2 || uploads != 1 || attempted == tc.revoke || (sends == 1) == tc.revoke {
				t.Fatalf("queries=%d uploads=%d sends=%d attempted=%t revoke=%t", queries, uploads, sends, attempted, tc.revoke)
			}
		})
	}
}
