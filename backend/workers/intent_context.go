package workers

import (
	"strings"

	"github.com/vietbui/chat-quality-agent/chatmemory"
	"github.com/vietbui/chat-quality-agent/engine"
)

// intentClassifierInput is the 1:1 classifier's input. With recent turns of
// the session it reads like the group classifier's: the excerpt in
// <tin_gan_day>, then the message to label after "Tin mới:", so a short reply
// such as "đúng rồi" is judged against the bot's question it answers. Without
// turns the message is passed as is.
func intentClassifierInput(recent []chatmemory.RecentTurn, message string) string {
	if len(recent) == 0 {
		return message
	}
	var b strings.Builder
	b.WriteString("<tin_gan_day>\n")
	for _, t := range recent {
		b.WriteString(t.Speaker)
		b.WriteString(": ")
		b.WriteString(engine.UntrustedText(t.Text))
		b.WriteByte('\n')
	}
	b.WriteString("</tin_gan_day>\n")
	b.WriteString("Tin mới: ")
	b.WriteString(engine.UntrustedText(message))
	return b.String()
}
