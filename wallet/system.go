package wallet

import "sort"

// NewSystem 以给定初始余额构造系统。
func NewSystem(initial map[WalletID]int64) *System {
	s := &System{wallets: make(map[WalletID]*walletState, len(initial))}
	for id, bal := range initial {
		s.wallets[id] = &walletState{
			balance: bal,
			pending: make(map[*pendingTransfer]struct{}),
		}
	}
	return s
}

// lockAll 按 WalletID 字典序依次加锁，保证全局统一的加锁顺序以避免死锁，
// 同时让 Snapshot 与提交在同一锁序下互斥，从而观察不到部分提交。
func (s *System) lockAll(ids []WalletID) []*walletState {
	uniq := make([]WalletID, 0, len(ids))
	seen := make(map[WalletID]struct{}, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		uniq = append(uniq, id)
	}
	sort.Slice(uniq, func(i, j int) bool { return uniq[i] < uniq[j] })
	states := make([]*walletState, 0, len(uniq))
	for _, id := range uniq {
		if w, ok := s.wallets[id]; ok {
			w.mu.Lock()
			states = append(states, w)
		}
	}
	return states
}

func unlockAll(states []*walletState) {
	for _, w := range states {
		w.mu.Unlock()
	}
}

// SetFrozen 设置钱包冻结状态；冻结时原子撤销其全部未提交付款并释放预占，
// 无需等待复核器返回；解冻不恢复任何旧转账。
func (s *System) SetFrozen(id WalletID, frozen bool) {
	w, ok := s.wallets[id]
	if !ok {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.frozen = frozen
	if frozen {
		for pt := range w.pending {
			w.reserved -= pt.total
			delete(w.pending, pt)
		}
	}
}

// Snapshot 返回指定钱包在同一逻辑时刻的状态。
func (s *System) Snapshot(ids []WalletID) map[WalletID]SnapshotEntry {
	states := s.lockAll(ids)
	defer unlockAll(states)
	out := make(map[WalletID]SnapshotEntry, len(ids))
	for _, id := range ids {
		if _, done := out[id]; done {
			continue
		}
		w, ok := s.wallets[id]
		if !ok {
			continue
		}
		out[id] = SnapshotEntry{
			Balance:   w.balance,
			Reserved:  w.reserved,
			Available: w.balance - w.reserved,
			Fees:      w.fees,
			Frozen:    w.frozen,
		}
	}
	return out
}
