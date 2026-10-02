// Package wallet 实现进程内 USDT 批量转账库，支持 PIN 复核与冻结撤销。
package wallet

import "sync"

// Recipient 表示一笔收款。
type Recipient struct {
	To     WalletID
	Amount int64 // USDT 最小单位
}

// Result 区分转账结果。
type Result int

const (
	// Success 复核通过且批次已原子提交。
	Success Result = iota
	// Rejected 复核拒绝，或付款钱包冻结/余额不足导致未发起复核。
	Rejected
	// Revoked 转账在提交前被冻结撤销。
	Revoked
)

// SnapshotEntry 是单个钱包在某一逻辑时刻的状态。
type SnapshotEntry struct {
	Balance   int64 // 账面余额
	Reserved  int64 // 预占额
	Available int64 // 可用余额 = Balance - Reserved
	Fees      int64 // 累计已收取手续费
	Frozen    bool
}

// System 是一组钱包及转账操作的集合。
type System struct {
	wallets map[WalletID]*walletState
}

// walletState 是单个钱包的内部状态。
type walletState struct {
	mu       sync.Mutex
	balance  int64
	reserved int64
	fees     int64
	frozen   bool
	pending  map[*pendingTransfer]struct{}
}

// pendingTransfer 表示一笔尚未提交的付款预占。
type pendingTransfer struct {
	total int64
}
