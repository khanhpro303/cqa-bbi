package engine

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/pancake"
	"github.com/vietbui/chat-quality-agent/pancakelabels"
)

var (
	pancakeIndexOnce sync.Once
	pancakeIndex     *pancakelabels.PSIDIndex
)

// PancakePSIDIndex is shared by scheduled and manual reconcile scans.
func PancakePSIDIndex() *pancakelabels.PSIDIndex {
	pancakeIndexOnce.Do(func() { pancakeIndex = pancakelabels.NewPSIDIndex() })
	return pancakeIndex
}

func (s *Scheduler) reconcilePancakeLabelsTask() {
	if s.cfg == nil || s.cfg.EncryptionKey == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()
	if err := pancakelabels.RunScheduled(ctx, db.DB, s.cfg.EncryptionKey, PancakePSIDIndex()); err != nil {
		log.Printf("[pancake-labels] scheduled reconcile failed: %s", pancakelabels.ErrorMessage(pancake.ErrorKind(err)))
	}
}
