package wallet

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

// checkInvariants 校验一组钱包快照的资金守恒与不变量。
func checkInvariants(t *testing.T, snap map[WalletID]SnapshotEntry, initialTotal int64) {
	t.Helper()
	var sum int64
	for id, e := range snap {
		if e.Balance < 0 || e.Reserved < 0 || e.Available < 0 {
			t.Errorf("钱包 %s 出现负值: %+v", id, e)
		}
		if e.Available != e.Balance-e.Reserved {
			t.Errorf("钱包 %s 可用余额不等于账面减预占: %+v", id, e)
		}
		sum += e.Balance + e.Fees
	}
	if sum != initialTotal {
		t.Errorf("资金不守恒: 账面+手续费 = %d, 初始 = %d", sum, initialTotal)
	}
}

// TestConcurrentTransfersAndSnapshots 并发执行对向批量转账、冻结切换与全覆盖
// Snapshot，校验资金守恒、无负余额、无部分提交。
func TestConcurrentTransfersAndSnapshots(t *testing.T) {
	ids := []WalletID{"W0", "W1", "W2", "W3"}
	initial := map[WalletID]int64{}
	for _, id := range ids {
		initial[id] = 1000 * unit
	}
	const initialTotal = 4 * 1000 * unit
	sys := NewSystem(initial)

	const iters = 200
	var stop int32
	var wg sync.WaitGroup

	// 对向批量转账：Wi -> W(i+1), W(i+2)，与反向转账并发。
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			from := ids[i]
			batch := []Recipient{
				{To: ids[(i+1)%4], Amount: unit},
				{To: ids[(i+2)%4], Amount: 2 * unit},
			}
			for n := 0; n < iters; n++ {
				verifier := func() bool {
					runtime.Gosched() // 放宽竞争窗口，不依赖睡眠
					return n%3 != 0   // 部分通过、部分拒绝
				}
				sys.Transfer(from, batch, verifier)
			}
		}(i)
	}

	// 冻结/解冻切换，与在途转账竞争。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for n := 0; n < iters && atomic.LoadInt32(&stop) == 0; n++ {
			sys.SetFrozen("W1", n%2 == 0)
			runtime.Gosched()
		}
		sys.SetFrozen("W1", false)
	}()

	// 全覆盖 Snapshot：任何时刻都不得观察到部分提交或资金不守恒。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for n := 0; n < iters && atomic.LoadInt32(&stop) == 0; n++ {
			checkInvariants(t, sys.Snapshot(ids), initialTotal)
			runtime.Gosched()
		}
	}()

	wg.Wait()
	sys.SetFrozen("W1", false)

	// 全部结束后：无残留预占，守恒仍成立。
	final := sys.Snapshot(ids)
	for _, id := range ids {
		if e := final[id]; e.Reserved != 0 {
			t.Errorf("结束后钱包 %s 仍有预占 %d", id, e.Reserved)
		}
	}
	checkInvariants(t, final, initialTotal)
}

// TestRejectVsFreezeNoDoubleRelease 验证正常拒绝与冻结竞争时预占只释放一次。
func TestRejectVsFreezeNoDoubleRelease(t *testing.T) {
	const balance = 100 * unit
	sys := NewSystem(map[WalletID]int64{"R": balance, "S": 0})
	recips := []Recipient{{To: "S", Amount: unit}}

	const iters = 500
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < iters; n++ {
				r := sys.Transfer("R", recips, func() bool {
					runtime.Gosched()
					return false // 正常拒绝
				})
				if r == Success {
					t.Errorf("拒绝的复核器不应产生 Success")
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for n := 0; n < iters; n++ {
			sys.SetFrozen("R", n%2 == 0)
			runtime.Gosched()
		}
	}()
	wg.Wait()
	sys.SetFrozen("R", false)

	// 若预占被重复释放，reserved 会变负、账面与可用余额不再一致。
	snap := sys.Snapshot([]WalletID{"R", "S"})
	if r := snap["R"]; r.Balance != balance || r.Reserved != 0 || r.Fees != 0 {
		t.Fatalf("竞争结束后 R = %+v, 期望账面 %d、预占 0、手续费 0", r, balance)
	}
	if s := snap["S"]; s.Balance != 0 {
		t.Fatalf("竞争结束后 S = %+v, 期望未收到任何款项", s)
	}
}
