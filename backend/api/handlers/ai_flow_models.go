package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
)

type aiFlowModelView struct {
	Role              string              `json:"role"`
	FlowID            string              `json:"flow_id"`
	Name              string              `json:"name"`
	Model             string              `json:"model"`
	Provider          string              `json:"provider"`
	Options           []aiFlowModelChoice `json:"options"`
	Source            string              `json:"source"`
	Managed           bool                `json:"managed"`
	ManagementProblem string              `json:"management_problem,omitempty"`
	SharedRoles       []string            `json:"shared_roles"`
}

type aiFlowModelChoice struct {
	Name     string `json:"name"`
	Provider string `json:"provider"`
}

var aiFlowRoles = []string{"internal", "public"}
var langflowModelWrite = make(chan struct{}, 1)

func ListAIFlowModels(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 12*time.Second)
	defer cancel()
	connection, others, err := loadAIFlowOwnership(ctx, middleware.GetTenantID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load_langflow_settings_failed", "message": "Không thể tải cài đặt kết nối Langflow."})
		return
	}

	flows := make(map[string]aiFlowModelView, len(aiFlowRoles))
	for _, role := range aiFlowRoles {
		view := describeAIFlow(connection, others, role)
		if view.FlowID != "" && connection.APIURL != "" &&
			(connection.APIKeySource != engine.LangflowSourceEnv || view.Source == engine.LangflowSourceEnv) {
			if internal, ok := flows["internal"]; ok && internal.FlowID == view.FlowID {
				view.Name, view.Provider, view.Model, view.Options = internal.Name, internal.Provider, internal.Model, internal.Options
				if internal.Model == "" {
					view.Managed = false
					view.ManagementProblem = internal.ManagementProblem
				}
				flows[role] = view
				continue
			}
			flow, err := engine.ReadLangflowFlowModel(ctx, connection.APIURL, connection.APIKey, view.FlowID)
			if err != nil {
				_, view.ManagementProblem = aiFlowModelError(err)
				view.Managed = false
			} else {
				setAIFlowModel(&view, flow)
			}
		}
		flows[role] = view
	}
	c.JSON(http.StatusOK, gin.H{"flows": flows})
}

func SaveAIFlowModel(c *gin.Context) {
	role := c.Param("role")
	if role != "internal" && role != "public" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_flow_role"})
		return
	}
	var req struct {
		Model    string `json:"model" binding:"required"`
		Provider string `json:"provider" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	select {
	case langflowModelWrite <- struct{}{}:
		defer func() { <-langflowModelWrite }()
	case <-ctx.Done():
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "langflow_sync_busy", "message": "Đang có lượt đồng bộ khác. Hãy thử lại."})
		return
	}

	tenantID := middleware.GetTenantID(c)
	connection, others, err := loadAIFlowOwnership(ctx, tenantID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load_langflow_settings_failed", "message": "Không thể tải cài đặt kết nối Langflow."})
		return
	}
	view := describeAIFlow(connection, others, role)
	if !view.Managed {
		c.JSON(http.StatusConflict, gin.H{"error": "flow_read_only", "message": view.ManagementProblem})
		return
	}
	flow, err := engine.SyncLangflowFlowModel(ctx, connection.APIURL, connection.APIKey, view.FlowID, strings.TrimSpace(req.Provider), strings.TrimSpace(req.Model))
	if err != nil {
		code, message := aiFlowModelError(err)
		status := http.StatusBadGateway
		if _, ok := engine.IsLangflowFlowModelError(err); ok {
			status = http.StatusUnprocessableEntity
		}
		c.JSON(status, gin.H{"error": code, "message": message})
		return
	}
	setAIFlowModel(&view, flow)
	db.LogActivity(tenantID, middleware.GetUserID(c), middleware.GetUserEmail(c), "ai.flow_model_synced",
		"settings", view.FlowID, strings.Join(view.SharedRoles, ", ")+" "+view.Provider+"/"+view.Model, "", c.ClientIP())
	c.JSON(http.StatusOK, gin.H{"flow": view, "updated_roles": view.SharedRoles})
}

func describeAIFlow(connection engine.LangflowConfig, others []engine.LangflowConfig, role string) aiFlowModelView {
	flowID, source := connection.FlowFor("public")
	if role == "internal" {
		flowID, source = connection.FlowFor("private")
	}
	flowID = canonicalAIFlowID(flowID)
	view := aiFlowModelView{Role: role, FlowID: flowID, Source: source, Options: []aiFlowModelChoice{}, SharedRoles: []string{role}}
	internal, _ := connection.FlowFor("private")
	public, _ := connection.FlowFor("public")
	if flowID != "" && canonicalAIFlowID(internal) == canonicalAIFlowID(public) {
		view.SharedRoles = aiFlowRoles
	}
	if flowID == "" || connection.APIURL == "" {
		view.ManagementProblem = "Hãy lưu URL và Flow ID tại AI Engine trước."
		return view
	}
	if source != engine.LangflowSourceTenant || connection.APIURLSource != engine.LangflowSourceTenant || connection.APIKeySource != engine.LangflowSourceTenant {
		view.ManagementProblem = "Để đồng bộ, hãy lưu URL, token và Flow ID riêng của tenant tại AI Engine."
		return view
	}
	if _, err := uuid.Parse(flowID); err != nil {
		view.ManagementProblem = "Hãy lưu Flow ID dạng UUID tại AI Engine."
		return view
	}
	for _, other := range others {
		// Flow UUIDs also match when the same server is reached via different
		// DNS names (e.g. the Docker hostname and the external reverse proxy).
		if canonicalAIFlowID(other.FlowID) == flowID || canonicalAIFlowID(other.PublicFlowID) == flowID {
			view.ManagementProblem = "Flow này được dùng bởi tenant khác. Hãy cấu hình Flow ID riêng trước khi đồng bộ."
			return view
		}
	}
	view.Managed = true
	return view
}

func canonicalAIFlowID(id string) string {
	id = strings.TrimSpace(id)
	if value, err := uuid.Parse(id); err == nil {
		return value.String()
	}
	return id
}

func setAIFlowModel(view *aiFlowModelView, flow engine.LangflowFlowModel) {
	view.Name, view.Model, view.Provider = flow.Name, flow.Model, flow.Provider
	for _, option := range flow.Options {
		if strings.EqualFold(option.Provider, flow.Provider) {
			view.Options = append(view.Options, aiFlowModelChoice{Name: option.Name, Provider: option.Provider})
		}
	}
}

func loadAIFlowOwnership(ctx context.Context, tenantID string) (engine.LangflowConfig, []engine.LangflowConfig, error) {
	cfg, err := config.Load()
	if err != nil {
		return engine.LangflowConfig{}, nil, err
	}
	database := db.DB.WithContext(ctx)
	var rows []models.AppSetting
	if err := database.Where("setting_key IN ?", engine.LangflowSettingKeys).
		Where("tenant_id = ? OR setting_key <> ?", tenantID, "ai_engine_langflow_token").Find(&rows).Error; err != nil {
		return engine.LangflowConfig{}, nil, err
	}
	byTenant := make(map[string]map[string]models.AppSetting)
	for _, row := range rows {
		if byTenant[row.TenantID] == nil {
			byTenant[row.TenantID] = make(map[string]models.AppSetting)
		}
		byTenant[row.TenantID][row.SettingKey] = row
	}
	connection := engine.ResolveTenantLangflowConfig(byTenant[tenantID], cfg)
	if connection.APIKeySource == engine.LangflowSourceEnv && connection.APIURL != strings.TrimRight(strings.TrimSpace(cfg.LangflowAPIURL), "/") {
		// A tenant-supplied endpoint must never receive the shared server key.
		connection.APIKey = ""
		connection.APIKeySource = engine.LangflowSourceNone
	}
	if address, err := url.Parse(connection.APIURL); err != nil || connection.APIURL != "" &&
		(address.Host == "" || address.User != nil || address.RawQuery != "" || address.Fragment != "" || (address.Scheme != "http" && address.Scheme != "https")) {
		return engine.LangflowConfig{}, nil, errors.New("invalid Langflow URL")
	}
	var tenantIDs []string
	if err := database.Model(&models.Tenant{}).Where("id <> ?", tenantID).Pluck("id", &tenantIDs).Error; err != nil {
		return engine.LangflowConfig{}, nil, err
	}
	others := make([]engine.LangflowConfig, 0, len(tenantIDs))
	for _, id := range tenantIDs {
		others = append(others, engine.ResolveTenantLangflowConfig(byTenant[id], cfg))
	}
	var channels []models.Channel
	if err := database.Select("tenant_id", "metadata").
		Where("tenant_id <> ? AND channel_type = ? AND metadata IS NOT NULL", tenantID, "zalo_oa").
		Find(&channels).Error; err != nil {
		return engine.LangflowConfig{}, nil, err
	}
	for _, channel := range channels {
		var metadata map[string]any
		if err := json.Unmarshal([]byte(channel.Metadata), &metadata); err != nil {
			continue
		}
		flowID, _ := metadata["langflow_flow_id"].(string)
		publicFlowID, _ := metadata["langflow_public_flow_id"].(string)
		if flowID != "" || publicFlowID != "" {
			overrides := engine.LangflowOverrides{FlowID: flowID, PublicFlowID: publicFlowID}
			others = append(others, engine.ResolveLangflowConfig(overrides, byTenant[channel.TenantID], cfg))
		}
	}
	return connection, others, nil
}

func aiFlowModelError(err error) (code, message string) {
	if modelErr, ok := engine.IsLangflowFlowModelError(err); ok {
		return modelErr.Code, modelErr.Message
	}
	var apiErr *engine.LangflowAPIError
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return "langflow_access_denied", "Langflow từ chối truy cập. Hãy kiểm tra token và quyền chỉnh sửa flow tại AI Engine."
		case http.StatusNotFound:
			return "langflow_flow_missing", "Langflow không tìm thấy flow. Hãy kiểm tra Flow ID tại AI Engine."
		case http.StatusConflict, http.StatusPreconditionFailed:
			return "langflow_flow_changed", "Flow vừa được chỉnh sửa. Tải lại model hiện tại rồi thử lại."
		}
	}
	return "langflow_request_failed", "Không thể xác nhận model trên Langflow. Tải lại model hiện tại và kiểm tra kết nối trước khi thử lại."
}
