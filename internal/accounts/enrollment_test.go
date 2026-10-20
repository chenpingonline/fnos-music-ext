package accounts

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestCancelledLoginCannotPublishAccount(t *testing.T) {
	manager, err := New(filepath.Join(t.TempDir(), "private", "accounts.json"), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = manager.CommitLogin(ctx, Account{ID: "cancelled", Name: "Cancelled", BridgeURL: "native://netease/cancelled"}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(manager.Snapshot().Accounts) != 0 {
		t.Fatal("cancelled account published")
	}
}
