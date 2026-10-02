package wallet

import "testing"

// TestBlockedVerifierDoesNotBlockOthers 验证：一个复核器阻塞期间，
// 对该钱包的收款、Snapshot、冻结以及不相交钱包间的转账都能完成。
func TestBlockedVerifierDoesNotBlockOthers(t *testing.T) {
	sys := NewSystem(map[WalletID]int64{
		"A": 10 * unit, "B": 0,
		"D": 5 * unit, "E": 3 * unit, "F": 0,
	})

	entered := make(chan struct{})
	release := make(chan bool)
	done := make(chan Result, 1)
	go func() {
		done <- sys.Transfer("A", []Recipient{{To: "B", Amount: unit}}, blockingVerifier(entered, release))
	}()
	<-entered // A 有一笔在途付款，复核器阻塞中

	// 对 A 的收款可完成（冻结只禁付款，收款不受在途复核影响）。
	if r := sys.Transfer("D", []Recipient{{To: "A", Amount: 2 * unit}}, func() bool { return true }); r != Success {
		t.Fatalf("D->A 结果 = %v, 期望 Success", r)
	}

	// Snapshot 不被阻塞。
	snap := sys.Snapshot([]WalletID{"A", "D"})
	if a := snap["A"]; a.Balance != 12*unit || a.Reserved != unit+1000 {
		t.Fatalf("Snapshot A = %+v, 期望账面 12 USDT、预占 %d", a, unit+1000)
	}

	// 不相交钱包间的转账可完成。
	if r := sys.Transfer("E", []Recipient{{To: "F", Amount: unit}}, func() bool { return true }); r != Success {
		t.Fatalf("E->F 结果 = %v, 期望 Success", r)
	}

	// 冻结可立即完成并撤销在途预占。
	sys.SetFrozen("A", true)
	if got := sys.Snapshot([]WalletID{"A"})["A"]; got.Reserved != 0 || !got.Frozen {
		t.Fatalf("冻结后 A = %+v, 期望预占 0 且已冻结", got)
	}

	release <- true
	if r := <-done; r != Revoked {
		t.Fatalf("被撤销转账结果 = %v, 期望 Revoked", r)
	}

	// 冻结期间 A 不得付款，但仍可收款。
	if r := sys.Transfer("A", []Recipient{{To: "B", Amount: unit}}, func() bool { return true }); r != Rejected {
		t.Fatalf("冻结中付款结果 = %v, 期望 Rejected", r)
	}
	if r := sys.Transfer("D", []Recipient{{To: "A", Amount: unit}}, func() bool { return true }); r != Success {
		t.Fatalf("冻结中收款结果 = %v, 期望 Success", r)
	}

	final := sys.Snapshot([]WalletID{"A", "B", "D", "E", "F"})
	if a := final["A"]; a.Balance != 13*unit || a.Reserved != 0 {
		t.Fatalf("最终 A = %+v", a)
	}
	if final["B"].Balance != 0 || final["F"].Balance != unit {
		t.Fatalf("最终 B=%+v F=%+v", final["B"], final["F"])
	}
}
