package handlers

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/vietbui/chat-quality-agent/channels"
)

func TestPersonalZaloDirectReplySignsEveryImage(t *testing.T) {
	const key = "0123456789abcdef0123456789abcdef"
	req := personalZaloChatbotRequest{
		MultipleImages: true, TenantID: "tenant", ChannelID: "channel", AccountExternalID: "account",
		ThreadID: "thread", SenderID: "sender", MessageID: "message", SentAt: time.Now().UTC(),
	}
	images := []personalZaloKnowledgeImage{
		{ID: "product-one", ZaloImage: channels.ZaloImage{Data: []byte("product image")}, product: true},
		{ID: "knowledge-two", ZaloImage: channels.ZaloImage{Data: []byte("knowledge image")}},
	}
	response := gin.H{"reply": "Goby", "images": images}
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	writePersonalZaloDirectReply(context, key, req, response, personalZaloERPIdentity{ID: "staff", AgentType: "private"}, personalZaloDirectGrant{
		grantHash: "permission-hash", permission: "permission-token", agentToken: "agent-token", turnID: "fixture-turn",
	})
	if recorder.Code != 200 {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		DeliveryToken string `json:"delivery_token"`
		Images        []struct {
			ID            string `json:"id"`
			DeliveryToken string `json:"delivery_token"`
		} `json:"images"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Images) != 2 || payload.DeliveryToken == "" || payload.Images[0].DeliveryToken == "" || payload.Images[1].DeliveryToken == "" {
		t.Fatalf("missing delivery tokens: %+v", payload)
	}
	if payload.Images[0].DeliveryToken == payload.Images[1].DeliveryToken || payload.Images[0].DeliveryToken == payload.DeliveryToken {
		t.Fatal("each reply part must have an independently bound token")
	}
	parse := func(raw string) personalZaloDeliveryClaims {
		t.Helper()
		var claims personalZaloDeliveryClaims
		_, err := jwt.ParseWithClaims(raw, &claims, func(*jwt.Token) (any, error) { return personalZaloDeliveryKey(key), nil },
			jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired(), jwt.WithSubject(personalZaloDeliveryBinding(req)))
		if err != nil {
			t.Fatal(err)
		}
		return claims
	}
	textClaims := parse(payload.DeliveryToken)
	if textClaims.TextDigest != personalZaloIdentityDigest("Goby") || textClaims.ImageID != "" {
		t.Fatalf("top-level token did not stay text-only: %+v", textClaims)
	}
	for index, want := range []struct {
		id, source string
		data       []byte
	}{
		{"product-one", "product", []byte("product image")},
		{"knowledge-two", "", []byte("knowledge image")},
	} {
		claims := parse(payload.Images[index].DeliveryToken)
		if claims.TextDigest != "" || claims.ImageID != want.id || claims.ImageSource != want.source ||
			claims.ImageDigest != personalZaloIdentityDigest(string(want.data)) ||
			claims.Permissions != "permission-hash" || claims.Credential != personalZaloIdentityDigest("agent-token") {
			t.Fatalf("image %d has wrong claims: %+v", index, claims)
		}
	}
	if !bytes.Equal(images[0].Data, []byte("product image")) {
		t.Fatal("signing mutated the attachment payload")
	}
}
