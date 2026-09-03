package lifecycle

import "testing"

func TestWindowLedgerKeepsRunningWhenLastWindowClosesByDefault(t *testing.T) {
	ledger := NewWindowLedger(true, "workspace", "manager")
	ledger.SetVisible("workspace", true)
	ledger.SetVisible("manager", true)

	first := ledger.RequestClose("workspace")
	if first.Action != WindowCloseToTray || first.LastVisible {
		t.Fatalf("first close = %#v, want tray and not last", first)
	}
	last := ledger.RequestClose("manager")
	if last.Action != WindowCloseToTray || !last.LastVisible {
		t.Fatalf("last close = %#v, want tray and last", last)
	}
	if ledger.IsQuitting() {
		t.Fatal("ledger entered quitting state for tray policy")
	}
}

func TestWindowLedgerQuitsAfterLastWindowWhenPolicyDisabled(t *testing.T) {
	ledger := NewWindowLedger(false, "workspace", "manager")
	ledger.SetVisible("workspace", true)
	ledger.SetVisible("manager", true)

	first := ledger.RequestClose("workspace")
	if first.Action != WindowCloseToTray || first.LastVisible {
		t.Fatalf("first close = %#v, want tray and not last", first)
	}
	last := ledger.RequestClose("manager")
	if last.Action != WindowCloseQuit || !last.LastVisible {
		t.Fatalf("last close = %#v, want quit and last", last)
	}
	if !ledger.IsQuitting() {
		t.Fatal("ledger did not enter quitting state")
	}
}

func TestWindowLedgerExplicitQuitWinsOverClosePolicy(t *testing.T) {
	ledger := NewWindowLedger(true, "workspace")
	ledger.SetVisible("workspace", true)
	ledger.BeginQuit()

	decision := ledger.RequestClose("workspace")
	if decision.Action != WindowCloseQuit {
		t.Fatalf("close after BeginQuit = %#v, want quit", decision)
	}
}

func TestWindowLedgerCanRecoverAfterFailedQuit(t *testing.T) {
	ledger := NewWindowLedger(true, "workspace", "settings")
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
