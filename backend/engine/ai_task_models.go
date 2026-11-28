package engine

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/vietbui/chat-quality-agent/ai"
)

const (
	AITaskQC                    = "qc_analysis"
	AITaskClassification        = "classification"
	AITaskProductGrouping       = "product_grouping"
	AITaskCRMAnalysis           = "crm_analysis"
	AITaskOrderApproval         = "cloudify_order_approval"
	AITaskKnowledgeSummary      = "knowledge_document_summary"
	AITaskKnowledgeImageCaption = "knowledge_image_caption"
	AITaskModelSettingPrefix    = "ai_task_model_"
)

type AITaskModel struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

type AITaskModelSetting struct {
	Key string `json:"key"`
	AITaskModel
}

type AITaskModelOption struct {
	AITaskModelSetting
	Label      string `json:"label"`
	Group      string `json:"group"`
	Capability string `json:"capability"`
}

func AITaskModelOptions() []AITaskModelOption {
	return []AITaskModelOption{
		{AITaskModelSetting{Key: AITaskQC}, "Chấm điểm chất lượng CSKH", "Phân tích", "text"},
		{AITaskModelSetting{Key: AITaskClassification}, "Phân loại hội thoại và Messenger Insights", "Phân tích", "text"},
		{AITaskModelSetting{Key: AITaskProductGrouping}, "Gom nhóm tên sản phẩm", "Phân tích", "text"},
		{AITaskModelSetting{Key: AITaskCRMAnalysis}, "Phân tích lịch sử chat CRM", "Phân tích", "text"},
		{AITaskModelSetting{Key: PromptIntentClassifier}, "Phân loại tin nhắn 1:1", "Chatbot", "text"},
		{AITaskModelSetting{Key: PromptGroupClassifier}, "Phân loại tin nhắn nhóm", "Chatbot", "text"},
		{AITaskModelSetting{Key: PromptCasualReply}, "Trả lời xã giao", "Chatbot", "text"},
		{AITaskModelSetting{Key: PromptImageReader}, "Đọc ảnh khách gửi", "Chatbot", "vision"},
		{AITaskModelSetting{Key: PromptComplaintMatch}, "Phân loại và gom khiếu nại", "Chatbot", "text"},
		{AITaskModelSetting{Key: PromptChatMemorySummary}, "Tóm tắt bộ nhớ khách hàng", "Chatbot", "text"},
		{AITaskModelSetting{Key: AITaskOrderApproval}, "Nhận diện duyệt đơn Cloudify", "ERP", "text"},
		{AITaskModelSetting{Key: PromptInventoryKeyword}, "Trích từ khóa tồn kho", "ERP", "text"},
		{AITaskModelSetting{Key: PromptProductCodeMatch}, "Đối chiếu mã sản phẩm", "ERP", "text"},
		{AITaskModelSetting{Key: PromptAttributeMatch}, "Đối chiếu thuộc tính sản phẩm", "ERP", "text"},
		{AITaskModelSetting{Key: AITaskKnowledgeSummary}, "Tóm tắt tài liệu kho tri thức", "Kho tri thức", "text"},
		{AITaskModelSetting{Key: AITaskKnowledgeImageCaption}, "Mô tả ảnh tài liệu kho tri thức", "Kho tri thức", "vision"},
	}
}

var aiTaskModelID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_./:@-]{0,99}$`)

// NormalizeAITaskModel validates a model override. An empty pair inherits the default.
func NormalizeAITaskModel(value AITaskModel) (AITaskModel, error) {
	value.Provider = strings.ToLower(strings.TrimSpace(value.Provider))
	value.Model = strings.TrimSpace(value.Model)
	if value == (AITaskModel{}) {
		return value, nil
	}
	if !ai.ValidProvider(value.Provider) {
		return value, fmt.Errorf("nhà cung cấp model không hợp lệ")
	}
	modelID := value.Model
	if value.Provider == "9router" {
		modelID = strings.TrimSuffix(modelID, "[1m]")
	}
	if !aiTaskModelID.MatchString(modelID) {
		return value, fmt.Errorf("mã model không hợp lệ")
	}
	return value, nil
}

func ParseAITaskModel(value string) (AITaskModel, error) {
	if strings.TrimSpace(value) == "" {
		return AITaskModel{}, nil
	}
	var setting AITaskModel
	if err := json.Unmarshal([]byte(value), &setting); err != nil {
		return AITaskModel{}, err
	}
	return NormalizeAITaskModel(setting)
}
