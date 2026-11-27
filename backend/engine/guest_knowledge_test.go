package engine

import (
	"context"
	"errors"
	"testing"
)

func TestGuestKnowledgeVerificationBypassesLangflow(t *testing.T) {
	for _, text := range []string{"FF901 giá?", "BAO GIA FF901", "Còn size L không?", "Đơn nào sắp giao?", "Xem công nợ"} {
		t.Run(text, func(t *testing.T) {
			result, err := RunGuestKnowledge(context.Background(), nil, LangflowConfig{}, "", "", "", text)
			if err != nil || result.Text != GuestKnowledgeVerificationReply {
				t.Fatalf("got %q, %v", result.Text, err)
			}
		})
	}
	for _, text := range []string{"Hướng dẫn đổi size?", "Chính sách báo giá thế nào?", "Chính sách xử lý đơn hết hàng thế nào?", "Các trạng thái đơn hàng nghĩa là gì?"} {
		t.Run(text, func(t *testing.T) {
			if guestKnowledgeRequiresVerification(text) {
				t.Fatal("general advice should go to the public knowledge flow")
			}
		})
	}
}

func TestGuestKnowledgeRejectsSharedOrPrivateFlowConfiguration(t *testing.T) {
	const publicID = "37593e48-052f-4be3-abcc-94a21465b3aa"
	good := LangflowConfig{
		APIURL: "https://langflow.example", APIKey: "tenant-key",
		APIKeySource: LangflowSourceTenant, PublicFlowIDSource: LangflowSourceTenant,
		FlowID: "private", PublicFlowID: publicID,
	}
	for name, change := range map[string]func(*LangflowConfig){
		"private fallback":  func(flow *LangflowConfig) { flow.PublicFlowID = "" },
		"shared API key":    func(flow *LangflowConfig) { flow.APIKeySource = LangflowSourceEnv },
		"shared flow":       func(flow *LangflowConfig) { flow.PublicFlowIDSource = LangflowSourceEnv },
		"same private flow": func(flow *LangflowConfig) { flow.FlowID = "{37593E48-052F-4BE3-ABCC-94A21465B3AA}" },
	} {
		t.Run(name, func(t *testing.T) {
			flow := good
			change(&flow)
			_, err := RunGuestKnowledge(context.Background(), nil, flow, "site-key", "tenant", "scope", "Chính sách đổi trả?")
			if !errors.Is(err, ErrGuestKnowledgeNotConfigured) {
				t.Fatalf("got %v, want guest configuration error", err)
			}
		})
	}
}
