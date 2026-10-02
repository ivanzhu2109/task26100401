// Package usdtwallet 提供进程内 USDT 钱包批量转账能力，
// 支持 PIN 复核、冻结撤销与一致性快照。
package usdtwallet

import (
	"sort"
	"sync"
)

// UnitsPerUSDT 是 1 USDT 对应的最小单位数。
const UnitsPerUSDT = 1_000_000

// MaxRecipients 是每批转账允许的最大收款钱包数。
const MaxRecipients = 50

// VerifyPIN 是复核器函数，返回 true 表示复核通过，false 表示拒绝。
// 复核器可能长时间阻塞，调用方不得假设其返回时机。
type VerifyPIN func() bool

// Result 区分一次 Transfer 的最终结果。
type Result int

const (
	// Success 表示整批转账已原子提交。
	Success Result = iota
	// Rejected 表示预占前检查未通过，或复核器拒绝了本次转账。
	Rejected
	// Revoked 表示转账在提交前被冻结操作撤销。
	Revoked
)

// Snapshot 是单个钱包在某一逻辑时刻的状态视图。
type Snapshot struct {
	Balance   int64 // 账面余额
	Held      int64 // 预占额
	Available int64 // 可用余额，恒等于 Balance - Held
	Fees      int64 // 累计已收取手续费（付款钱包名下）
	Frozen    bool  // 冻结状态
}

// System 是一组钱包及其转账服务的集合。
type System struct {
	wallets map[string]*walletState
}

// walletState 是单个钱包的内部状态，全部字段由其自身互斥锁保护。
type walletState struct {
	mu      sync.Mutex
	balance int64
	held    int64
	fees    int64
	frozen  bool
	pending map[*transfer]struct{}
}

// transfer 是一笔尚未提交的付款预占记录。
// 其状态归属付款钱包的锁保护：只要还存在于 sender.pending 中即为有效。
type transfer struct {
	sender   *walletState
	senderID string
	recips   []*walletState
	recipIDs []string
	amounts  []int64 // 与 recips 一一对应的收款金额
	total    int64   // 预占总额：全部本金 + 手续费
}

// NewSystem 以给定的钱包及初始余额构造系统。
func NewSystem(balances map[string]int64) *System {
	s := &System{wallets: make(map[string]*walletState, len(balances))}
	for id, bal := range balances {
		s.wallets[id] = &walletState{
			balance: bal,
			pending: make(map[*transfer]struct{}),
		}
	}
	return s
}

// fee 按金额的 0.1% 向上取整到最小单位计算手续费。
func fee(amount int64) int64 {
	return (amount + 999) / 1000
}

// lockWallets 按钱包 ID 字典序对给定钱包依次加锁，
// 返回对应的解锁函数。全局统一的加锁顺序保证多锁场景不会死锁。
// ids 中允许重复，重复的钱包只会被加锁一次。
func (s *System) lockWallets(ids []string) (locked []*walletState, unlock func()) {
	sorted := make([]string, len(ids))
	copy(sorted, ids)
	sort.Strings(sorted)
	var prev string
	for i, id := range sorted {
		if i > 0 && id == prev {
			continue
		}
		w := s.wallets[id]
		w.mu.Lock()
		locked = append(locked, w)
		prev = id
	}
	return locked, func() {
		for _, w := range locked {
			w.mu.Unlock()
		}
	}
}
