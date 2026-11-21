package workers

import (
	"strings"

	"github.com/vietbui/chat-quality-agent/chatmemory"
	"github.com/vietbui/chat-quality-agent/engine"
)

// intentClassifierInput is the 1:1 classifier's input, always in the same
// server-built shape: the recent turns of the session in <tin_gan_day> (an
// empty block when there are none), then the message to label after
// "Tin mới:". Every piece of chat text is flattened to one line without angle
// brackets, so a message cannot fake a speaker, the block or the boundary.
func intentClassifierInput(recent []chatmemory.RecentTurn, message string) string {
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
