package pancakelabels

import (
	"context"
	"errors"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pancake"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/gorm"
)

const (
	listenerTick = 30 * time.Second
	leaseFor     = 90 * time.Second
)

// Listener keeps one pancake.vn realtime socket per tenant (a headless
// replacement for Pancake webhooks). A database lease makes sure only one
// replica listens per tenant. Reconcile scans repair anything it misses.
type Listener struct {
	DB        *gorm.DB
	Key       string
	SocketURL string
	Index     *PSIDIndex
	owner     string
	mu        sync.Mutex
	running   map[string]*tenantSocket
}

type tenantSocket struct {
	cancel context.CancelFunc
	pages  string
}

func NewListener(database *gorm.DB, key, socketURL string) *Listener {
	return &Listener{DB: database, Key: key, SocketURL: socketURL, Index: NewPSIDIndex(), owner: pkg.NewUUID(), running: map[string]*tenantSocket{}}
}

// Run blocks until ctx ends.
func (l *Listener) Run(ctx context.Context) {
	ticker := time.NewTicker(listenerTick)
	defer ticker.Stop()
	for {
		l.reconcileSockets(ctx)
		select {
		case <-ctx.Done():
			l.mu.Lock()
			for _, socket := range l.running {
				socket.cancel()
			}
			l.mu.Unlock()
			return
		case <-ticker.C:
		}
	}
}

type pageBinding struct {
	TenantID      string
	ChannelID     string
	PancakePageID string
}

func (l *Listener) wanted() (map[string][]pageBinding, error) {
	var rows []pageBinding
	err := l.DB.Model(&models.PancakeLabelState{}).
		Select("pancake_label_states.tenant_id, pancake_label_states.channel_id, COALESCE(NULLIF(pancake_label_states.pancake_page_id,''), channels.external_id) AS pancake_page_id").
		Joins("JOIN channels ON channels.id = pancake_label_states.channel_id AND channels.tenant_id = pancake_label_states.tenant_id").
		Joins("JOIN pancake_connections ON pancake_connections.tenant_id = pancake_label_states.tenant_id").
		Where("pancake_label_states.enabled = ? AND channels.is_active = ? AND channels.channel_type = ?", true, true, "facebook").
		Where("pancake_connections.user_token_encrypted IS NOT NULL AND LENGTH(pancake_connections.user_token_encrypted) > 0").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	byTenant := map[string][]pageBinding{}
	for _, row := range rows {
		if row.PancakePageID != "" {
			byTenant[row.TenantID] = append(byTenant[row.TenantID], row)
		}
	}
	return byTenant, nil
}

func pageKey(bindings []pageBinding) string {
	ids := make([]string, 0, len(bindings))
	for _, b := range bindings {
		ids = append(ids, b.PancakePageID+"="+b.ChannelID)
	}
	sort.Strings(ids)
	return strings.Join(ids, ",")
}

func (l *Listener) reconcileSockets(ctx context.Context) {
	wanted, err := l.wanted()
	if err != nil {
		log.Printf("[pancake-socket] cannot list Pages: %v", err)
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for tenantID, socket := range l.running {
		bindings, ok := wanted[tenantID]
		if !ok || pageKey(bindings) != socket.pages {
			socket.cancel()
			delete(l.running, tenantID)
		}
	}
	for tenantID, bindings := range wanted {
		if _, ok := l.running[tenantID]; ok {
			continue
		}
		socketCtx, cancel := context.WithCancel(ctx)
		l.running[tenantID] = &tenantSocket{cancel: cancel, pages: pageKey(bindings)}
		go l.runTenant(socketCtx, tenantID, bindings)
	}
}

// claim takes or renews the tenant lease. It returns false when another
// replica owns a live lease.
func (l *Listener) claim(tenantID string, now time.Time) bool {
	result := l.DB.Model(&models.PancakeConnection{}).
		Where("tenant_id = ? AND (lease_owner = ? OR lease_until IS NULL OR lease_until < ?)", tenantID, l.owner, now).
		Updates(map[string]any{"lease_owner": l.owner, "lease_until": now.Add(leaseFor)})
	return result.Error == nil && result.RowsAffected == 1
}

func (l *Listener) setStatus(tenantID, status, message string, extra map[string]any) {
	updates := map[string]any{"socket_status": status, "socket_error": truncate(message, 250)}
	for k, v := range extra {
		updates[k] = v
	}
	l.DB.Model(&models.PancakeConnection{}).Where("tenant_id = ? AND lease_owner = ?", tenantID, l.owner).Updates(updates)
}

func socketMessage(err error) (string, time.Duration) {
	var socketErr *pancake.SocketError
	if errors.As(err, &socketErr) {
		switch socketErr.Kind {
		case "auth":
			return "Pancake từ chối User access token; hãy dán token mới.", 10 * time.Minute
		case "join_refused":
			return "Pancake không cho nghe Page; kiểm tra tài khoản có quyền trên Page.", 5 * time.Minute
		case "protocol":
			return "Địa chỉ websocket Pancake không hợp lệ.", 30 * time.Minute
		}
	}
	return "Mất kết nối websocket Pancake; đang nối lại.", 0
}

func (l *Listener) runTenant(ctx context.Context, tenantID string, bindings []pageBinding) {
	channelsByPage := map[string]models.Channel{}
	pageIDs := []string{}
	for _, binding := range bindings {
		var channel models.Channel
		if err := l.DB.Where("id = ? AND tenant_id = ?", binding.ChannelID, tenantID).First(&channel).Error; err != nil {
			continue
		}
		if _, dup := channelsByPage[binding.PancakePageID]; !dup {
			pageIDs = append(pageIDs, binding.PancakePageID)
		}
		channelsByPage[binding.PancakePageID] = channel
	}
	if len(pageIDs) == 0 {
		return
	}
	backoff := time.Second
	for ctx.Err() == nil {
		if !l.claim(tenantID, time.Now()) {
			if !sleep(ctx, leaseFor) {
				return
			}
			continue
		}
		conn, err := LoadConnection(l.DB, tenantID)
		token := UserToken(conn, l.Key)
		if err != nil || token == "" {
			l.setStatus(tenantID, "error", "Không đọc được User access token Pancake.", nil)
			if !sleep(ctx, 5*time.Minute) {
				return
			}
			continue
		}
		renewCtx, stopRenew := context.WithCancel(ctx)
		go func() {
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-renewCtx.Done():
					return
				case <-ticker.C:
					if !l.claim(tenantID, time.Now()) {
						stopRenew()
						return
					}
				}
			}
		}()
		connected := time.Time{}
		var lastEventWrite time.Time
		err = pancake.RunSocket(renewCtx, pancake.SocketConfig{
			URL: l.SocketURL, UserToken: token, UserID: conn.PancakeUserID, PageIDs: pageIDs,
			OnConnected: func() {
				connected = time.Now()
				l.setStatus(tenantID, "connected", "", map[string]any{"socket_connected_at": connected})
			},
			OnEvent: func(pageID string, frame pancake.Frame) {
				channel, ok := channelsByPage[pageID]
				if !ok {
					return
				}
				updates := pancake.ExtractUpdates(pageID, frame.Payload)
				if len(updates) == 0 {
					return
				}
				now := time.Now()
				for _, update := range updates {
					convID := l.Index.Lookup(l.DB, channel, update.PSID, 10*time.Minute)
					if _, err := Apply(ctx, l.DB, channel, update, SourceSocket, convID, now); err != nil {
						log.Printf("[pancake-socket] cannot store tags for channel %s: %s", channel.ID, ErrorMessage(pancake.ErrorKind(err)))
					}
				}
				if now.Sub(lastEventWrite) > 10*time.Second {
					lastEventWrite = now
					l.DB.Model(&models.PancakeConnection{}).Where("tenant_id = ?", tenantID).Update("socket_event_at", now)
				}
			},
		})
		stopRenew()
		if ctx.Err() != nil {
			l.setStatus(tenantID, "disconnected", "", map[string]any{"lease_until": nil})
			return
		}
		message, wait := socketMessage(err)
		status := "disconnected"
		if wait > 0 {
			status = "error"
		} else {
			if !connected.IsZero() && time.Since(connected) > 5*time.Minute {
				backoff = time.Second
			}
			wait = backoff
			if backoff < time.Minute {
				backoff *= 2
			}
		}
		if err == nil {
			message = ""
		}
		log.Printf("[pancake-socket] tenant %s socket ended (%s); retry in %s", tenantID, status, wait)
		l.setStatus(tenantID, status, message, nil)
		if !sleep(ctx, wait) {
			return
		}
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
