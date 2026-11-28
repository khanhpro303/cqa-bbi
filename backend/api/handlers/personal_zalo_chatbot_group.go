package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"gorm.io/gorm"
)

const (
	personalZaloStaffFollowup = 5 * time.Minute
	personalZaloDeliveryTTL   = 2 * time.Minute
	personalZaloStaffSuffix   = ":staff_engaged"
)

type personalZaloGroupTurn struct {
	Inferred bool
	Staff    bool
	Speaker  string
}

type personalZaloPendingReply struct {
	SessionID string
	MessageID string
	Line      string
	Staff     bool
}

func preparePersonalZaloGroupTurn(ctx context.Context, channel models.Channel, session personalZaloSessionConfig, req personalZaloChatbotRequest, addressed bool, needsReply func(context.Context, string, string) bool) personalZaloGroupTurn {
	if req.ThreadType != personalZaloThreadGroup || (!session.GroupAssistantMode && !addressed) {
		return personalZaloGroupTurn{}
	}
	welcome := personalZaloMatchesKeyword(req.Text, session.SessionKeyword)
	goodbye := personalZaloMatchesKeyword(req.Text, session.SessionEndKeyword)
	if welcome || goodbye {
		clearPersonalZaloStaffFollowup(ctx, req)
	}
	if goodbye {
		return personalZaloGroupTurn{}
	}

	staff, err := isPersonalZaloGroupStaff(ctx, channel, req.SenderID)
	if err != nil {
		log.Printf("[personal-zalo] could not check staff assignments for channel %s: %v", channel.ID, err)
		return personalZaloGroupTurn{}
	}
	speaker := personalZaloGroupSpeaker(req, staff)
	if !session.GroupAssistantMode || db.RedisClient == nil {
		return personalZaloGroupTurn{Staff: staff, Speaker: speaker}
	}
	recent, err := rememberPersonalZaloGroupLine(ctx, req, session, speaker+": "+personalZaloGroupText(req.Text))
	if err != nil {
		log.Printf("[personal-zalo] could not read group context for channel %s: %v", channel.ID, err)
		return personalZaloGroupTurn{Staff: staff}
	}
	// Staff discussing a customer request stay silent until they have called
	// the bot. Their messages still belong in the recent group context.
	if !addressed && staff && session.GroupStaffCallOnly {
		sessionKey := personalZaloSessionKey(req)
		state, err := db.RedisClient.MGet(ctx, sessionKey, sessionKey+personalZaloStaffSuffix).Result()
		if err != nil || state[0] == nil || state[0] != state[1] {
			return personalZaloGroupTurn{Staff: staff}
		}
	}

	turn := personalZaloGroupTurn{Staff: staff, Speaker: speaker}
	if !addressed {
		input := personalZaloGroupContext("tin_gan_day", recent) + "Tin mới: " + speaker + ": " + personalZaloGroupText(req.Text)
		turn.Inferred = needsReply(ctx, req.TenantID, input)
	}
	return turn
}

func isPersonalZaloGroupStaff(ctx context.Context, channel models.Channel, senderID string) (bool, error) {
	owners, err := decodePersonalZaloAccountOwners(channel.Metadata)
	if err != nil {
		return false, err
	}
	for _, owner := range owners {
		if owner.AccountExternalID == senderID && owner.UserID != "" {
			var member models.UserTenant
			err := db.DB.WithContext(ctx).Select("user_id").
				Where("tenant_id = ? AND user_id = ?", channel.TenantID, owner.UserID).Take(&member).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return false, nil
			}
			return err == nil, err
		}
	}
	return false, nil
}

func personalZaloGroupSpeaker(req personalZaloChatbotRequest, staff bool) string {
	person := sha256.Sum256([]byte(personalZaloLegacyFlowScope(req) + "\x00" + req.SenderID))
	role := "chưa xác thực"
	if staff {
		role = "nhân viên"
	}
	return fmt.Sprintf("Thành viên %x (%s)", person[:4], role)
}

func personalZaloGroupText(text string) string {
	text = engine.UntrustedText(text)
	return text[:personalZaloTextBoundary(text, 500)]
}

func personalZaloGroupContext(tag string, lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return "<" + tag + ">\n" + strings.Join(lines, "\n") + "\n</" + tag + ">\n"
}

func rememberPersonalZaloGroupLine(ctx context.Context, req personalZaloChatbotRequest, session personalZaloSessionConfig, line string) ([]string, error) {
	key := "personal_zalo_group_recent:" + personalZaloLegacyFlowScope(req)
	ttl := min(30*time.Minute, time.Duration(session.SessionTimeoutMinutes)*time.Minute)
	pipe := db.RedisClient.TxPipeline()
	recent := pipe.LRange(ctx, key, 0, -1)
	pipe.RPush(ctx, key, line)
	pipe.LTrim(ctx, key, -12, -1)
	pipe.Expire(ctx, key, ttl)
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}
	return recent.Result()
}

func clearPersonalZaloStaffFollowup(ctx context.Context, req personalZaloChatbotRequest) {
	if req.ThreadType == personalZaloThreadGroup && db.RedisClient != nil {
		_ = db.RedisClient.Del(ctx, personalZaloSessionKey(req)+personalZaloStaffSuffix).Err()
	}
}

func awaitPersonalZaloGroupDelivery(ctx context.Context, req personalZaloChatbotRequest, turn personalZaloGroupTurn, reply string) string {
	sessionKey := personalZaloSessionKey(req)
	sessionID, err := db.RedisClient.Get(ctx, sessionKey).Result()
	if err != nil {
		return ""
	}
	record, err := json.Marshal(personalZaloPendingReply{
		SessionID: sessionID,
		MessageID: req.MessageID,
		Line:      "Bot → " + turn.Speaker + ": " + personalZaloGroupText(reply),
		Staff:     turn.Staff,
	})
	if err != nil {
		return ""
	}
	receipt := uuid.NewString()
	if err := db.RedisClient.Set(ctx, sessionKey+":delivery:"+receipt, record, personalZaloDeliveryTTL).Err(); err != nil {
		log.Printf("[personal-zalo] could not stage group delivery for channel %s: %v", req.ChannelID, err)
		return ""
	}
	return receipt
}

func confirmPersonalZaloGroupReply(ctx context.Context, session personalZaloSessionConfig, req personalZaloChatbotRequest) {
	if !session.GroupAssistantMode || db.RedisClient == nil {
		return
	}
	sessionKey := personalZaloSessionKey(req)
	raw, err := db.RedisClient.GetDel(ctx, sessionKey+":delivery:"+req.DeliveryReceipt).Bytes()
	if errors.Is(err, redis.Nil) {
		return
	}
	if err != nil {
		log.Printf("[personal-zalo] could not confirm group delivery for channel %s: %v", req.ChannelID, err)
		return
	}
	var record personalZaloPendingReply
	if json.Unmarshal(raw, &record) != nil || record.MessageID != req.MessageID {
		return
	}
	if currentSession, err := db.RedisClient.Get(ctx, sessionKey).Result(); err != nil || currentSession != record.SessionID {
		return
	}
	if _, err := rememberPersonalZaloGroupLine(ctx, req, session, record.Line); err != nil {
		log.Printf("[personal-zalo] could not store group response for channel %s: %v", req.ChannelID, err)
	}
	if record.Staff && session.GroupStaffCallOnly {
		ttl := min(personalZaloStaffFollowup, time.Duration(session.SessionTimeoutMinutes)*time.Minute)
		if err := db.RedisClient.Set(ctx, sessionKey+personalZaloStaffSuffix, record.SessionID, ttl).Err(); err != nil {
			log.Printf("[personal-zalo] could not store staff follow-up window for channel %s: %v", req.ChannelID, err)
		}
	}
}
