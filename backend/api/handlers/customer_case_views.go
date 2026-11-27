package handlers

import (
	"gorm.io/gorm"

	"github.com/vietbui/chat-quality-agent/customercases"
	"github.com/vietbui/chat-quality-agent/db/models"
)

type complaintChannel struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ChannelType string `json:"channel_type"`
	Metadata    string `json:"-"`
}

type complaintStreamOption struct {
	Key       string `json:"key"`
	Name      string `json:"name"`
	ChannelID string `json:"channel_id"`
}

type customerCaseView struct {
	models.CustomerCase
	ChannelName     string `json:"channel_name,omitempty"`
	StreamName      string `json:"stream_name,omitempty"`
	SourceGroupName string `json:"source_group_name,omitempty"`
	ConversationID  string `json:"conversation_id,omitempty"`
}

func listComplaintChannels(conn *gorm.DB, tenantID string) ([]complaintChannel, error) {
	channels := make([]complaintChannel, 0)
	err := conn.Model(&models.Channel{}).Select("id, name, channel_type, metadata").
		Where("tenant_id = ? AND channel_type IN ?", tenantID, []string{"zalo_oa", "personal_zalo_import"}).
		Order("name, id").Find(&channels).Error
	return channels, err
}

func complaintStreamOptions(channel complaintChannel) []complaintStreamOption {
	streams := customercases.ParseSettings(channel.Metadata).Streams
	options := make([]complaintStreamOption, 0, len(streams))
	for _, stream := range streams {
		options = append(options, complaintStreamOption{Key: stream.Key, Name: stream.Name, ChannelID: channel.ID})
	}
	return options
}

func listComplaintStreamFilters(conn *gorm.DB, tenantID, channelID string, channels []complaintChannel) ([]complaintStreamOption, error) {
	options := make([]complaintStreamOption, 0)
	if channelID == "" {
		return options, nil
	}
	for _, channel := range channels {
		if channel.ID == channelID {
			options = complaintStreamOptions(channel)
			break
		}
	}
	// Retired streams remain filterable while staff can only move a ticket
	// to a stream present in the current channel settings.
	var history []string
	if err := conn.Model(&models.CustomerCase{}).Where("tenant_id = ? AND channel_id = ? AND stream <> ''", tenantID, channelID).
		Distinct().Order("stream").Pluck("stream", &history).Error; err != nil {
		return nil, err
	}
	configured := make(map[string]bool, len(options))
	for _, option := range options {
		configured[option.Key] = true
	}
	for _, stream := range history {
		if !configured[stream] {
			options = append(options, complaintStreamOption{Key: stream, Name: stream, ChannelID: channelID})
		}
	}
	return options, nil
}

type caseConversationRef struct {
	channelID  string
	externalID string
}

func buildCustomerCaseViews(conn *gorm.DB, tenantID string, rows []models.CustomerCase, channels []complaintChannel) ([]customerCaseView, error) {
	channelsByID := make(map[string]complaintChannel, len(channels))
	settingsByID := make(map[string]customercases.Settings, len(channels))
	for _, channel := range channels {
		channelsByID[channel.ID] = channel
		settingsByID[channel.ID] = customercases.ParseSettings(channel.Metadata)
	}
	views := make([]customerCaseView, 0, len(rows))
	conversationRefs := make([]caseConversationRef, 0, len(rows))
	conversationPairs := make([][]string, 0, len(rows))
	groupPairs := make([][]string, 0, len(rows))
	for _, row := range rows {
		if row.ZaloGroupID == "" && row.SubjectType == "group" {
			row.ZaloGroupID = row.SubjectID
		}
		channel := channelsByID[row.ChannelID]
		views = append(views, customerCaseView{CustomerCase: row, ChannelName: channel.Name,
			StreamName: customercases.StreamLabel(settingsByID[row.ChannelID], row.Stream)})
		threadType, externalID := "user", row.ZaloUserID
		if externalID == "" {
			externalID = row.SubjectID
		}
		if row.ZaloGroupID != "" {
			threadType, externalID = "group", row.ZaloGroupID
			if row.ChannelID != "" {
				groupPairs = append(groupPairs, []string{row.ChannelID, row.ZaloGroupID})
			}
		}
		if externalID != "" && channel.ChannelType == "personal_zalo_import" {
			externalID = composeImportConversationID(threadType, externalID)
		}
		conversationRefs = append(conversationRefs, caseConversationRef{row.ChannelID, externalID})
		if row.ChannelID != "" && externalID != "" {
			conversationPairs = append(conversationPairs, []string{row.ChannelID, externalID})
		}
	}
	conversations := make(map[caseConversationRef]models.Conversation)
	if len(conversationPairs) > 0 {
		var matched []models.Conversation
		if err := conn.Select("id, channel_id, external_conversation_id, customer_name").Where("tenant_id = ?", tenantID).
			Where("(channel_id, external_conversation_id) IN ?", conversationPairs).Find(&matched).Error; err != nil {
			return nil, err
		}
		for _, conversation := range matched {
			conversations[caseConversationRef{conversation.ChannelID, conversation.ExternalConversationID}] = conversation
		}
	}
	groupNames := make(map[caseConversationRef]string)
	if len(groupPairs) > 0 {
		var groups []models.CRMGroup
		if err := conn.Unscoped().Select("name, channel_id, zalo_group_id").Where("tenant_id = ?", tenantID).
			Where("(channel_id, zalo_group_id) IN ?", groupPairs).Order("archived_at DESC, updated_at, id").Find(&groups).Error; err != nil {
			return nil, err
		}
		for _, group := range groups {
			groupNames[caseConversationRef{group.ChannelID, group.ZaloGroupID}] = group.Name
		}
	}
	for i := range views {
		view := &views[i]
		conversation := conversations[conversationRefs[i]]
		view.ConversationID = conversation.ID
		if view.ZaloGroupID != "" {
			view.SourceGroupName = firstNonEmpty(conversation.CustomerName, groupNames[caseConversationRef{view.ChannelID, view.ZaloGroupID}])
			if view.SourceGroupName == "" && view.SubjectType == "group" {
				view.SourceGroupName = view.CustomerName
			}
		}
	}
	return views, nil
}
