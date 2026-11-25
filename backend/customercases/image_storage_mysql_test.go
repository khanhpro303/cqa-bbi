package customercases

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
)

var testImageHeader = []byte("\x89PNG\r\n\x1a\n")

func TestStageImageAndRecordCaseEvidenceMySQL(t *testing.T) {
	connectTestMySQL(t)
	ctx := context.Background()
	tenantID, channelID := setupTenant(t)
	stubAlerts(t)
	stubModel(t, &fakeModel{})

	original := append(bytes.Clone(testImageHeader), "original\x00image\xff\x01\"\x1a"...)
	duplicate := append(bytes.Clone(testImageHeader), "duplicate upload"...)
	imageID, err := StageImage(ctx, tenantID, channelID, "zu-image", "turn-image", "https://f1.zdn.vn/evidence.png", "image/png", original)
	if err != nil {
		t.Fatal(err)
	}
	duplicateID, err := StageImage(ctx, tenantID, channelID, "zu-image", "turn-image", "https://f1.zdn.vn/evidence.png", "image/png", duplicate)
	if err != nil {
		t.Fatal(err)
	}
	if duplicateID != imageID {
		t.Fatalf("duplicate stage id = %q, want %q", duplicateID, imageID)
	}

	var stored []models.CustomerImageFile
	if err := db.DB.Where("tenant_id = ? AND id = ?", tenantID, imageID).Find(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || !bytes.Equal(stored[0].Data, original) || stored[0].MediaType != "image/png" {
		t.Fatalf("staged image rows = %+v", stored)
	}

	out, err := Record(ctx, Report{
		TenantID: tenantID, ChannelID: channelID, SubjectType: "user", SubjectID: "zu-image", ZaloUserID: "zu-image",
		Source: SourceBot, Title: "Mũ bị nứt", TurnID: "turn-image",
		Images: []models.CaseImage{{ImageID: imageID, URL: "https://f1.zdn.vn/evidence.png", Description: "Mũ bị nứt"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if images := ParseImages(out.Case.Images); len(images) != 1 || images[0].ImageID != imageID || images[0].URL != "" {
		t.Fatalf("case images should only expose the private image ID: %+v", images)
	}
	var links []models.CustomerCaseImageLink
	if err := db.DB.Where("case_id = ? AND image_id = ?", out.Case.ID, imageID).Find(&links).Error; err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 {
		t.Fatalf("case image links = %+v", links)
	}

	got, err := ReadCaseImage(ctx, tenantID, out.Case.ID, imageID, time.Now())
	if err != nil || !bytes.Equal(got.Data, original) || got.MediaType != "image/png" {
		t.Fatalf("read linked image = %+v, %v", got, err)
	}
	if _, err := ReadCaseImage(ctx, pkg.NewUUID(), out.Case.ID, imageID, time.Now()); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("cross-tenant read error = %v", err)
	}
	if _, err := ReadCaseImage(ctx, tenantID, pkg.NewUUID(), imageID, time.Now()); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("other-case read error = %v", err)
	}

	unlinkedID, err := StageImage(ctx, tenantID, channelID, "zu-image", "turn-unlinked", "https://f1.zdn.vn/unlinked.png", "image/png", original)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCaseImage(ctx, tenantID, out.Case.ID, unlinkedID, time.Now()); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("staged-only read error = %v", err)
	}

	var event models.CustomerCaseEvent
	if err := db.DB.Where("case_id = ? AND kind = ?", out.Case.ID, "opened").Take(&event).Error; err != nil {
		t.Fatal(err)
	}
	if images := ParseImages(event.Images); len(images) != 1 || images[0].ImageID != imageID || images[0].URL != "" {
		t.Fatalf("case history should only expose the private image ID: %+v", images)
	}
}

func TestPurgeExpiredImagesMySQL(t *testing.T) {
	connectTestMySQL(t)
	ctx := context.Background()
	tenantID, channelID := setupTenant(t)
	now := time.Now().UTC().Truncate(time.Second)

	stage := func(turn, name string, age time.Duration) string {
		t.Helper()
		id, err := StageImage(ctx, tenantID, channelID, "zu-purge", turn, "https://f1.zdn.vn/"+name+".png", "image/png", testImageHeader)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.DB.Model(&models.CustomerImageFile{}).Where("id = ?", id).Update("created_at", now.Add(-age)).Error; err != nil {
			t.Fatal(err)
		}
		return id
	}
	makeCase := func(status string, resolvedAt *time.Time) string {
		t.Helper()
		id := pkg.NewUUID()
		cc := models.CustomerCase{
			ID: id, TenantID: tenantID, ChannelID: channelID, SubjectType: "user", SubjectID: "zu-purge",
			ZaloUserID: "zu-purge", Title: "Ảnh bằng chứng", Images: "[]", Status: status, Source: SourceBot,
			ResolvedAt: resolvedAt, CreatedAt: now.Add(-120 * 24 * time.Hour), UpdatedAt: now,
		}
		if err := db.DB.Create(&cc).Error; err != nil {
			t.Fatal(err)
		}
		return id
	}
	link := func(caseID, imageID string) {
		t.Helper()
		if err := db.DB.Create(&models.CustomerCaseImageLink{CaseID: caseID, ImageID: imageID}).Error; err != nil {
			t.Fatal(err)
		}
	}

	oldClosedAt := now.Add(-91 * 24 * time.Hour)
	oldUnattached := stage("turn-old-unattached", "old-unattached", 25*time.Hour)
	freshUnattached := stage("turn-fresh-unattached", "fresh-unattached", 23*time.Hour)
	closedOnly := stage("turn-closed", "closed", 100*24*time.Hour)
	recentlyClosed := stage("turn-recent-closed", "recently-closed", 100*24*time.Hour)
	openOnly := stage("turn-open", "open", 100*24*time.Hour)
	reopened := stage("turn-reopened", "reopened", 100*24*time.Hour)
	shared := stage("turn-shared", "shared", 100*24*time.Hour)

	closedCase := makeCase(StatusCancelled, &oldClosedAt)
	recentClosedAt := now.Add(-89 * 24 * time.Hour)
	recentClosedCase := makeCase(StatusCancelled, &recentClosedAt)
	openCase := makeCase(StatusOpen, nil)
	reopenedCase := makeCase(StatusOpen, nil)
	sharedClosedCase := makeCase(StatusCancelled, &oldClosedAt)
	sharedOpenCase := makeCase(StatusInProgress, nil)
	link(closedCase, closedOnly)
	link(recentClosedCase, recentlyClosed)
	link(openCase, openOnly)
	link(reopenedCase, reopened)
	link(sharedClosedCase, shared)
	link(sharedOpenCase, shared)

	if _, err := ReadCaseImage(ctx, tenantID, sharedClosedCase, shared, now); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("closed case should lose access even when a shared file is still retained: %v", err)
	}
	if _, err := ReadCaseImage(ctx, tenantID, recentClosedCase, recentlyClosed, now); err != nil {
		t.Fatalf("image should remain accessible for 90 days after close: %v", err)
	}
	if err := purgeExpiredImages(ctx, now); err != nil {
		t.Fatal(err)
	}

	assertPresent := func(id string, want bool) {
		t.Helper()
		var n int64
		if err := db.DB.Model(&models.CustomerImageFile{}).Where("id = ?", id).Count(&n).Error; err != nil {
			t.Fatal(err)
		}
		if (n == 1) != want {
			t.Fatalf("image %s present = %t, want %t", id, n == 1, want)
		}
	}
	assertPresent(oldUnattached, false)
	assertPresent(freshUnattached, true)
	assertPresent(closedOnly, false)
	assertPresent(recentlyClosed, true)
	assertPresent(openOnly, true)
	assertPresent(reopened, true)
	assertPresent(shared, true)
}
