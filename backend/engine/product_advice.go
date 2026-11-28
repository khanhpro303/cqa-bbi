package engine

import "strings"

// AddProductAdviceInstruction adds the tenant's product advice rules
// (task prompt chatbot_product_advice). It keeps the flow's configured
// default prompt when there is no system prompt override for the turn.
func AddProductAdviceInstruction(tenantID, input, prompt string) (string, string) {
	instruction := TaskPromptText(tenantID, PromptProductAdvice)
	if strings.TrimSpace(prompt) == "" {
		return input + "\n\n" + instruction, prompt
	}
	return input, prompt + "\n\n" + instruction
}
