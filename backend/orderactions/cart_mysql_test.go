package orderactions

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
)

func cartSeed() models.OrderCart {
	return models.OrderCart{TenantID: "tenant", WorkKey: "zalo_session:c:u", ChannelID: "c", ZaloUserID: "u", AgentType: "public"}
}

func addLine(line CartLine, quantity int) func(*models.OrderCart, *CartState) (bool, error) {
	return func(_ *models.OrderCart, s *CartState) (bool, error) {
		return s.Apply(line, OpAdd, quantity) != "", nil
	}
}

func TestCartLifecycle(t *testing.T) {
	testDatabase(t)
	ctx := context.Background()

	// Nothing matched: no empty cart is stored.
	if _, _, err := UpdateCart(ctx, cartSeed(), true, func(*models.OrderCart, *CartState) (bool, error) { return false, nil }); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := OpenCart(ctx, "tenant", "zalo_session:c:u"); ok {
		t.Fatal("an unchanged new cart must not be stored")
	}
	if _, _, err := UpdateCart(ctx, cartSeed(), false, addLine(storm(), 1)); !errors.Is(err, ErrNoCart) {
		t.Fatalf("update without create on no cart: %v", err)
	}

	first, _, err := UpdateCart(ctx, cartSeed(), true, addLine(storm(), 1))
	if err != nil {
		t.Fatal(err)
	}
	second, state, err := UpdateCart(ctx, cartSeed(), true, addLine(visor(), 1))
	if err != nil || second.ID != first.ID || len(state.Lines) != 2 {
		t.Fatalf("the chat keeps one cart: %v %s %s %+v", err, first.ID, second.ID, state)
	}
	cart, state, ok := OpenCart(ctx, "tenant", "zalo_session:c:u")
	if !ok || len(state.Lines) != 2 || state.Lines[0].Quantity != 1 || cart.ExpiresAt.Before(time.Now().Add(CartTTL-time.Minute)) {
		t.Fatalf("open cart: %+v %+v", cart, state)
	}

	// A confirmation sent for the cart is withdrawn when the cart changes.
	draftAction := &models.OrderAction{TenantID: "tenant", Kind: KindCreate, WorkKey: "zalo_session:c:u", ZaloUserID: "u", CartID: cart.ID, Payload: `{}`}
	if err := Save(ctx, draftAction); err != nil {
		t.Fatal(err)
	}
	if _, _, err := UpdateCart(ctx, cartSeed(), true, addLine(storm(), 1)); err != nil {
		t.Fatal(err)
	}
	if status(t, draftAction.ID) != StatusSuperseded {
		t.Fatalf("stale confirmation must be withdrawn, got %s", status(t, draftAction.ID))
	}

	// Confirming the new draft writes the order, closes the cart and sends
	// the closing message.
	confirm := &models.OrderAction{
		TenantID: "tenant", Kind: KindCreate, WorkKey: "zalo_session:c:u", ZaloUserID: "u", CartID: cart.ID,
		CustomerCode: "KH125", OrderCode: "CQA-261119-ABCDEF",
		Payload: `{"MA_HOA_DON":"CQA-261119-ABCDEF","DON_DAT_HANG_CHI_TIET":[{"SO_LUONG":2,"DON_GIA":2490000},{"SO_LUONG":1,"DON_GIA":350000}]}`,
	}
	if err := Save(ctx, confirm); err != nil {
		t.Fatal(err)
	}
	writer := &fakeWriter{response: `{"code":200,"message":"Success","data":{"id":25470,"MA_HOA_DON":"CQA-261119-ABCDEF"}}`}
	outcome, err := Commit(ctx, confirm.ID, "u", writer)
	if err != nil || outcome.Status != StatusDone || !strings.Contains(outcome.Message, "Đã lên đơn CQA-261119-ABCDEF") || !strings.Contains(outcome.Message, "5.330.000đ") {
		t.Fatalf("commit: %+v %v", outcome, err)
	}
	if _, _, ok := OpenCart(ctx, "tenant", "zalo_session:c:u"); ok {
		t.Fatal("an ordered cart is closed")
	}
	var stored models.OrderCart
	db.DB.First(&stored, "id = ?", cart.ID)
	if stored.Status != CartOrdered || stored.OrderCode != "CQA-261119-ABCDEF" || stored.OpenKey != nil {
		t.Fatalf("ordered cart: %+v", stored)
	}

	// The next order in the same chat starts a new cart.
	third, _, err := UpdateCart(ctx, cartSeed(), true, addLine(visor(), 1))
	if err != nil || third.ID == cart.ID {
		t.Fatalf("new cart after ordering: %v", err)
	}
	if cancelled, err := CancelCart(ctx, "tenant", "zalo_session:c:u"); err != nil || !cancelled {
		t.Fatalf("cancel: %v %v", cancelled, err)
	}
	if cancelled, _ := CancelCart(ctx, "tenant", "zalo_session:c:u"); cancelled {
		t.Fatal("nothing left to cancel")
	}

	// An expired cart is not reopened; a new one replaces it.
	expired, _, _ := UpdateCart(ctx, cartSeed(), true, addLine(visor(), 1))
	db.DB.Model(&models.OrderCart{}).Where("id = ?", expired.ID).Update("expires_at", time.Now().Add(-time.Minute))
	if _, _, ok := OpenCart(ctx, "tenant", "zalo_session:c:u"); ok {
		t.Fatal("expired cart must not be open")
	}
	fresh, state, err := UpdateCart(ctx, cartSeed(), true, addLine(storm(), 1))
	if err != nil || fresh.ID == expired.ID || len(state.Lines) != 1 || state.Lines[0].SKU != "SP1001" {
		t.Fatalf("replace expired cart: %v %+v", err, state)
	}
	var old models.OrderCart
	db.DB.First(&old, "id = ?", expired.ID)
	if old.Status != CartExpired || old.OpenKey != nil {
		t.Fatalf("expired cart row: %+v", old)
	}
}

func TestCartFailedCommitKeepsCart(t *testing.T) {
	testDatabase(t)
	ctx := context.Background()
	cart, _, err := UpdateCart(ctx, cartSeed(), true, addLine(storm(), 1))
	if err != nil {
		t.Fatal(err)
	}
	action := &models.OrderAction{TenantID: "tenant", Kind: KindCreate, WorkKey: "zalo_session:c:u", ZaloUserID: "u", CartID: cart.ID, Payload: `{}`}
	if err := Save(ctx, action); err != nil {
		t.Fatal(err)
	}
	outcome, _ := Commit(ctx, action.ID, "u", &fakeWriter{err: errors.New("connection reset")})
	if outcome.Status != StatusUnknown {
		t.Fatalf("outcome %+v", outcome)
	}
	if _, _, ok := OpenCart(ctx, "tenant", "zalo_session:c:u"); !ok {
		t.Fatal("a cart whose order did not go through stays open")
	}
}
