package lifecycle

import "testing"

func TestWindowLedgerCanRecoverAfterFailedQuit(t *testing.T) {
	ledger := NewWindowLedger("workspace", "settings")
	ledger.SetVisible("workspace", true)
	ledger.SetVisible("settings", true)
	ledger.BeginQuit()

	if ledger.VisibleCount() != 0 {
		t.Fatalf("visible count during quit = %d, want 0", ledger.VisibleCount())
	}
	if ledger.TryShow("workspace") {
		t.Fatal("window became visible while quit was in progress")
	}

	ledger.CancelQuit()
	if !ledger.TryShow("settings") {
		t.Fatal("window could not be shown after failed quit recovery")
	}
	if ledger.VisibleCount() != 1 {
		t.Fatalf("visible count after recovery = %d, want 1", ledger.VisibleCount())
	}
}
