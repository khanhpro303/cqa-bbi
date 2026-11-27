package handlers

import (
	"context"
	"fmt"
	"log"
	"slices"
	"time"

	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/workers"
	"golang.org/x/sync/errgroup"
)

func personalZaloProductReplyImages(ctx context.Context, perm *engine.GroupPermissionContext, chosen []models.ProductAdvisoryImage) []personalZaloKnowledgeImage {
	if perm == nil || len(chosen) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()

	// Bound concurrent decoding and remote requests, preserving the order
	// chosen by the agent and allowing text and other images to survive errors.
	images := make([]personalZaloKnowledgeImage, len(chosen))
	var loads errgroup.Group
	loads.SetLimit(4)
	for i, choice := range chosen {
		loads.Go(func() error {
			row, err := workers.LoadPersonalZaloProductAdviceImage(ctx, perm, choice.ID)
			if err != nil {
				log.Printf("[personal-zalo] could not load product image %s: %v", choice.ID, err)
				return nil
			}
			if row.ID == "" || row.ProductAdvisoryID != choice.ProductAdvisoryID {
				return nil
			}
			attachment, err := preparePersonalZaloProductImage(ctx, row)
			if err != nil {
				log.Printf("[personal-zalo] could not prepare product image %s: %v", row.ID, err)
				return nil
			}
			images[i] = personalZaloKnowledgeImage{ID: row.ID, ZaloImage: attachment, product: true}
			return nil
		})
	}
	_ = loads.Wait()
	return slices.DeleteFunc(images, func(image personalZaloKnowledgeImage) bool { return image.ID == "" })
}

func preparePersonalZaloProductImage(ctx context.Context, row models.ProductAdvisoryImage) (channels.ZaloImage, error) {
	switch row.Source {
	case "upload":
		return channels.PrepareZaloImage(row.Data)
	case "scraped":
		remote, err := channels.FetchPublicImage(ctx, row.URL)
		if err != nil {
			return channels.ZaloImage{}, err
		}
		return channels.PrepareZaloImage(remote.Bytes)
	default:
		return channels.ZaloImage{}, fmt.Errorf("unsupported product image source %q", row.Source)
	}
}
