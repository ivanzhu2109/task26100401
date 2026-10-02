package wallet

import "testing"

func TestFeeRounding(t *testing.T) {
	cases := []struct{ amount, want int64 }{
		{0, 0}, {1, 1}, {999, 1}, {1000, 1}, {1001, 2}, {1_000_000, 1000},
	}
	for _, c := range cases {
		if got := fee(c.amount); got != c.want {
			t.Errorf("fee(%d) = %d, 期望 %d", c.amount, got, c.want)
		}
	}
}

// TestRejectWithoutVerifier 余额不足或已冻结时直接拒绝，不得调用复核器。
func TestRejectWithoutVerifier(t *testing.T) {
	sys := NewSystem(map[WalletID]int64{"A": unit, "B": 0})
	called := false
	verifier := func() bool { called = true; return true }

	// 可用余额不足（本金+手续费超出余额）。
	if r := sys.Transfer("A", []Recipient{{To: "B", Amount: unit}}, verifier); r != Rejected {
		t.Fatalf("余额不足结果 = %v, 期望 Rejected", r)
	}
	if called {
		t.Fatal("余额不足时不得调用复核器")
	}

	// 已冻结。
	sys.SetFrozen("A", true)
	if r := sys.Transfer("A", []Recipient{{To: "B", Amount: 1}}, verifier); r != Rejected {
		t.Fatalf("冻结付款结果 = %v, 期望 Rejected", r)
	}
	if called {
		t.Fatal("冻结时不得调用复核器")
	}
}

// TestBatchCommitConservation 50 个收款的批次一次性提交后资金守恒。
func TestBatchCommitConservation(t *testing.T) {
	initial := map[WalletID]int64{"P": 1000 * unit}
	recips := make([]Recipient, 0, 50)
	var expectTotal int64
	for i := 0; i < 50; i++ {
		id := WalletID("R" + string(rune('A'+i%26)) + string(rune('0'+i/26)))
		initial[id] = 0
		recips = append(recips, Recipient{To: id, Amount: unit})
		expectTotal += unit + 1000
	}
	sys := NewSystem(initial)

	if r := sys.Transfer("P", recips, func() bool { return true }); r != Success {
		t.Fatalf("批量转账结果 = %v, 期望 Success", r)
	}

	ids := make([]WalletID, 0, 51)
	ids = append(ids, "P")
	for _, r := range recips {
		ids = append(ids, r.To)
	}
	snap := sys.Snapshot(ids)
	if p := snap["P"]; p.Balance != 1000*unit-expectTotal || p.Fees != 50*1000 || p.Reserved != 0 {
		t.Fatalf("提交后 P = %+v", p)
	}
	var sum int64
	for _, e := range snap {
		sum += e.Balance + e.Fees
	}
	if sum != 1000*unit {
		t.Fatalf("资金不守恒: %d != %d", sum, 1000*unit)
	}
}
