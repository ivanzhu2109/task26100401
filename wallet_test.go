package usdtwallet

import (
	"fmt"
	"sync"
	"testing"
)

// blockedVerifier 返回一对通道：entered 在复核器被调用后关闭，
// 向 release 发送的值作为复核结果。用于精确控制并发顺序。
func blockedVerifier() (entered chan struct{}, release chan bool, vf VerifyPIN) {
	entered = make(chan struct{})
	release = make(chan bool)
	vf = func() bool {
		close(entered)
		return <-release
	}
	return
}

func TestFreezeRevokesPendingAndOldCallbacksCannotAffectNew(t *testing.T) {
	sys := NewSystem(map[string]int64{"s": 100_000, "r1": 0, "r2": 0})

	type pending struct {
		release chan bool
		result  chan Result
	}
	start := func(recips map[string]int64) *pending {
		entered, release, vf := blockedVerifier()
		p := &pending{release: release, result: make(chan Result, 1)}
		go func() { p.result <- sys.Transfer("s", recips, vf) }()
		<-entered // 屏障：确认预占完成且复核器已阻塞
		return p
	}

	p1 := start(map[string]int64{"r1": 1000}) // 手续费 1，预占 1001
	p2 := start(map[string]int64{"r1": 2000}) // 手续费 2，预占 2002

	if got := sys.Snapshot([]string{"s"})["s"].Held; got != 3003 {
		t.Fatalf("预占后 held = %d, 期望 3003", got)
	}

	// 冻结：必须立即撤销全部在途付款并释放预占。
	sys.SetFrozen("s", true)
	snap := sys.Snapshot([]string{"s"})["s"]
	if snap.Held != 0 || !snap.Frozen || snap.Balance != 100_000 {
		t.Fatalf("冻结后快照 = %+v, 期望 held=0 frozen=true balance=100000", snap)
	}

	// 解冻后发起新预占。
	sys.SetFrozen("s", false)
	p3 := start(map[string]int64{"r2": 5000}) // 手续费 5，预占 5005
	if got := sys.Snapshot([]string{"s"})["s"].Held; got != 5005 {
		t.Fatalf("新预占后 held = %d, 期望 5005", got)
	}

	// 旧回调分别通过和拒绝：都不得扣款，也不得误释放新预占。
	p1.release <- true
	if r := <-p1.result; r != Revoked {
		t.Fatalf("旧回调(通过)结果 = %v, 期望 Revoked", r)
	}
	p2.release <- false
	if r := <-p2.result; r != Revoked {
		t.Fatalf("旧回调(拒绝)结果 = %v, 期望 Revoked", r)
	}
	snap = sys.Snapshot([]string{"s"})["s"]
	if snap.Held != 5005 || snap.Balance != 100_000 || snap.Fees != 0 {
		t.Fatalf("旧回调返回后快照 = %+v, 期望 held=5005 balance=100000 fees=0", snap)
	}

	// 新转账正常提交。
	p3.release <- true
	if r := <-p3.result; r != Success {
		t.Fatalf("新转账结果 = %v, 期望 Success", r)
	}
	snaps := sys.Snapshot([]string{"s", "r1", "r2"})
	if s := snaps["s"]; s.Balance != 94_995 || s.Held != 0 || s.Fees != 5 {
		t.Fatalf("提交后 s = %+v, 期望 balance=94995 held=0 fees=5", s)
	}
	if got := snaps["r1"].Balance; got != 0 {
		t.Fatalf("r1 balance = %d, 期望 0（旧转账不得到账）", got)
	}
	if got := snaps["r2"].Balance; got != 5000 {
		t.Fatalf("r2 balance = %d, 期望 5000", got)
	}
}

func TestBlockedVerifierDoesNotBlockOtherOps(t *testing.T) {
	sys := NewSystem(map[string]int64{
		"s1": 10_000, "r1": 0,
		"o1": 5_000,
		"w1": 3_000, "w2": 0,
	})

	entered, release, vf := blockedVerifier()
	res := make(chan Result, 1)
	go func() { res <- sys.Transfer("s1", map[string]int64{"r1": 4000}, vf) }()
	<-entered // s1 的复核器已阻塞，预占 4004

	// 对该钱包的收款能完成。
	if r := sys.Transfer("o1", map[string]int64{"s1": 1000}, func() bool { return true }); r != Success {
		t.Fatalf("收款转账结果 = %v, 期望 Success", r)
	}
	// Snapshot 能完成。
	snap := sys.Snapshot([]string{"s1"})["s1"]
	if snap.Balance != 11_000 || snap.Held != 4004 {
		t.Fatalf("收款后 s1 = %+v, 期望 balance=11000 held=4004", snap)
	}
	// 不相交钱包的转账能完成。
	if r := sys.Transfer("w1", map[string]int64{"w2": 500}, func() bool { return true }); r != Success {
		t.Fatalf("不相交转账结果 = %v, 期望 Success", r)
	}
	// 冻结能立即完成（若需等待复核器，测试将挂起超时）。
	sys.SetFrozen("s1", true)
	if got := sys.Snapshot([]string{"s1"})["s1"].Held; got != 0 {
		t.Fatalf("冻结后 held = %d, 期望 0", got)
	}

	release <- true
	if r := <-res; r != Revoked {
		t.Fatalf("被撤销转账结果 = %v, 期望 Revoked", r)
	}
	snaps := sys.Snapshot([]string{"s1", "r1", "w1", "w2"})
	if s := snaps["s1"]; s.Balance != 11_000 || s.Held != 0 {
		t.Fatalf("撤销后 s1 = %+v, 期望 balance=11000 held=0", s)
	}
	if got := snaps["r1"].Balance; got != 0 {
		t.Fatalf("r1 balance = %d, 期望 0", got)
	}
	if got := snaps["w2"].Balance; got != 500 {
		t.Fatalf("w2 balance = %d, 期望 500", got)
	}
}

func TestConcurrentOpposingTransfersAndSnapshots(t *testing.T) {
	ids := []string{"a", "b", "c", "d", "e", "f"}
	const initial = 1_000_000
	balances := make(map[string]int64, len(ids))
	var total int64
	for _, id := range ids {
		balances[id] = initial
		total += initial
	}
	sys := NewSystem(balances)

	var wg sync.WaitGroup
	start := make(chan struct{})
	// 对向批量转账：i -> i+1 与 i+1 -> i 并发执行。
	for i := 0; i < len(ids); i++ {
		from, to := ids[i], ids[(i+1)%len(ids)]
		wg.Add(1)
		go func(from, to string) {
			defer wg.Done()
			<-start
			for n := 0; n < 200; n++ {
				amount := int64(100 + n%50)
				sys.Transfer(from, map[string]int64{to: amount}, func() bool { return true })
			}
		}(from, to)
	}
	// 覆盖全部钱包的并发 Snapshot，校验守恒与不变量。
	for k := 0; k < 4; k++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for n := 0; n < 200; n++ {
				snap := sys.Snapshot(ids)
				var sum int64
				for _, id := range ids {
					w := snap[id]
					if w.Balance < 0 || w.Held < 0 || w.Available < 0 {
						t.Errorf("出现负值: %s = %+v", id, w)
						return
					}
					if w.Available != w.Balance-w.Held {
						t.Errorf("可用余额不变量被破坏: %s = %+v", id, w)
						return
					}
					sum += w.Balance + w.Fees
				}
				if sum != total {
					t.Errorf("资金不守恒: sum = %d, 期望 %d", sum, total)
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()

	snap := sys.Snapshot(ids)
	var sum int64
	for _, id := range ids {
		w := snap[id]
		if w.Held != 0 {
			t.Fatalf("结束后仍有预占: %s held = %d", id, w.Held)
		}
		sum += w.Balance + w.Fees
	}
	if sum != total {
		t.Fatalf("最终资金不守恒: sum = %d, 期望 %d", sum, total)
	}
}

func TestRejectFreezeRaceNoDoubleRelease(t *testing.T) {
	for iter := 0; iter < 500; iter++ {
		sys := NewSystem(map[string]int64{"s": 10_000, "r": 0})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			// 复核器始终拒绝；与冻结竞争时只能释放一次预占。
			sys.Transfer("s", map[string]int64{"r": 1000}, func() bool { return false })
		}()
		go func() {
			defer wg.Done()
			sys.SetFrozen("s", true)
			sys.SetFrozen("s", false)
		}()
		wg.Wait()

		snap := sys.Snapshot([]string{"s", "r"})
		s := snap["s"]
		if s.Held != 0 {
			t.Fatalf("iter %d: 结束后 held = %d（重复释放或泄漏）", iter, s.Held)
		}
		if s.Balance != 10_000 || s.Fees != 0 || snap["r"].Balance != 0 {
			t.Fatalf("iter %d: 拒绝的转账改变了余额: s = %+v, r = %+v", iter, s, snap["r"])
		}
	}
}

func TestFreezeCommitRaceIsAtomic(t *testing.T) {
	for iter := 0; iter < 500; iter++ {
		sys := NewSystem(map[string]int64{"s": 10_000, "r": 0})
		entered, release, vf := blockedVerifier()
		res := make(chan Result, 1)
		go func() { res <- sys.Transfer("s", map[string]int64{"r": 1000}, vf) }()
		<-entered // 屏障：预占已完成，复核器阻塞中

		sys.SetFrozen("s", true)
		release <- true
		r := <-res

		snap := sys.Snapshot([]string{"s", "r"})
		s, recv := snap["s"], snap["r"]
		if s.Held != 0 {
			t.Fatalf("iter %d: 结束后 held = %d, 期望 0", iter, s.Held)
		}
		switch r {
		case Success: // 完整提交在先
			if s.Balance != 8_999 || s.Fees != 1 || recv.Balance != 1000 {
				t.Fatalf("iter %d: 提交在先但状态错误: s = %+v, r = %+v", iter, s, recv)
			}
		case Revoked: // 冻结撤销在先
			if s.Balance != 10_000 || s.Fees != 0 || recv.Balance != 0 {
				t.Fatalf("iter %d: 撤销在先但状态错误: s = %+v, r = %+v", iter, s, recv)
			}
		default:
			t.Fatalf("iter %d: 意外结果 %v", iter, r)
		}
		if s.Balance+s.Fees+recv.Balance != 10_000 {
			t.Fatalf("iter %d: 资金不守恒", iter)
		}
	}
}

func TestSnapshotConsistentViewUnderBatchCommit(t *testing.T) {
	// 多收款批次提交期间，Snapshot 不得观察到部分到账。
	sys := NewSystem(map[string]int64{"s": 10_000_000, "r1": 0, "r2": 0, "r3": 0})
	ids := []string{"s", "r1", "r2", "r3"}
	const total = 10_000_000

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			snap := sys.Snapshot(ids)
			var sum int64
			for _, id := range ids {
				w := snap[id]
				if w.Available != w.Balance-w.Held || w.Available < 0 {
					t.Errorf("不变量被破坏: %s = %+v", id, w)
					return
				}
				sum += w.Balance + w.Fees
			}
			if sum != total {
				t.Errorf("观察到部分提交或资金不守恒: sum = %d", sum)
				return
			}
		}
	}()

	recips := map[string]int64{"r1": 1000, "r2": 2000, "r3": 3000}
	for n := 0; n < 300; n++ {
		if r := sys.Transfer("s", recips, func() bool { return true }); r != Success {
			t.Fatalf("转账结果 = %v, 期望 Success", r)
		}
	}
	close(stop)
	wg.Wait()
}

func ExampleSystem_Transfer() {
	sys := NewSystem(map[string]int64{"alice": 2 * UnitsPerUSDT, "bob": 0})
	r := sys.Transfer("alice", map[string]int64{"bob": UnitsPerUSDT}, func() bool { return true })
	snap := sys.Snapshot([]string{"alice", "bob"})
	fmt.Println(r == Success, snap["alice"].Balance, snap["alice"].Fees, snap["bob"].Balance)
	// Output: true 999000 1000 1000000
}
