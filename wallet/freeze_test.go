package wallet

import "testing"

const unit = 1_000_000 // 1 USDT

// blockingVerifier 返回一个复核器：进入时关闭 entered，随后阻塞等待 release 给出结果。
func blockingVerifier(entered chan<- struct{}, release <-chan bool) func() bool {
	return func() bool {
		close(entered)
		return <-release
	}
}

// TestFreezeRevokesPendingAndOldCallbacks 验证：
// 多个阻塞中的复核器被冻结立即撤销并释放预占；解冻后新预占不受旧回调影响。
func TestFreezeRevokesPendingAndOldCallbacks(t *testing.T) {
	const total = unit + 1000 // 本金 1 USDT + 手续费 1000
	sys := NewSystem(map[WalletID]int64{"A": 10 * unit, "B": 0, "C": 0})
	recips := func(to WalletID) []Recipient { return []Recipient{{To: to, Amount: unit}} }

	type handle struct {
		entered chan struct{}
		release chan bool
		done    chan Result
	}
	start := func(to WalletID) *handle {
		h := &handle{make(chan struct{}), make(chan bool), make(chan Result, 1)}
		go func() {
			h.done <- sys.Transfer("A", recips(to), blockingVerifier(h.entered, h.release))
		}()
		<-h.entered // 屏障：确保预占已完成且复核器正在阻塞
		return h
	}

	t1 := start("B")
	t2 := start("C")

	if got := sys.Snapshot([]WalletID{"A"})["A"]; got.Reserved != 2*total {
		t.Fatalf("两笔在途转账后预占 = %d, 期望 %d", got.Reserved, 2*total)
	}

	// 冻结：必须立即撤销全部在途付款并释放预占，不等复核器返回。
	sys.SetFrozen("A", true)
	if got := sys.Snapshot([]WalletID{"A"})["A"]; got.Reserved != 0 || !got.Frozen {
		t.Fatalf("冻结后预占 = %d, frozen = %v; 期望 0, true", got.Reserved, got.Frozen)
	}

	// 解冻后发起新预占。
	sys.SetFrozen("A", false)
	t3 := start("B")
	if got := sys.Snapshot([]WalletID{"A"})["A"]; got.Reserved != total {
		t.Fatalf("新转账预占 = %d, 期望 %d", got.Reserved, total)
	}

	// 旧回调分别通过和拒绝：都不得扣款，也不得释放新预占。
	t1.release <- true
	t2.release <- false
	if r := <-t1.done; r != Revoked {
		t.Fatalf("旧转账(通过)结果 = %v, 期望 Revoked", r)
	}
	if r := <-t2.done; r != Revoked {
		t.Fatalf("旧转账(拒绝)结果 = %v, 期望 Revoked", r)
	}
	if got := sys.Snapshot([]WalletID{"A"})["A"]; got.Reserved != total || got.Balance != 10*unit {
		t.Fatalf("旧回调后状态 = %+v, 期望预占 %d 且账面不变", got, total)
	}

	// 新转账正常提交。
	t3.release <- true
	if r := <-t3.done; r != Success {
		t.Fatalf("新转账结果 = %v, 期望 Success", r)
	}
	snap := sys.Snapshot([]WalletID{"A", "B", "C"})
	if a := snap["A"]; a.Balance != 10*unit-total || a.Reserved != 0 || a.Fees != 1000 {
		t.Fatalf("提交后 A = %+v", a)
	}
	if b := snap["B"]; b.Balance != unit {
		t.Fatalf("提交后 B = %+v", b)
	}
	if c := snap["C"]; c.Balance != 0 {
		t.Fatalf("提交后 C = %+v", c)
	}
}
