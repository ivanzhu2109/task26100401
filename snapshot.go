package usdtwallet

// Snapshot 返回指定钱包在同一逻辑时刻的状态视图。
//
// 实现按字典序同时锁定全部目标钱包，因此观察到的是
// 同一逻辑时刻的一致视图，不会看到部分提交。
func (s *System) Snapshot(ids []string) map[string]Snapshot {
	_, unlock := s.lockWallets(ids)
	defer unlock()
	out := make(map[string]Snapshot, len(ids))
	for _, id := range ids {
		if _, dup := out[id]; dup {
			continue
		}
		w := s.wallets[id]
		out[id] = Snapshot{
			Balance:   w.balance,
			Held:      w.held,
			Available: w.balance - w.held,
			Fees:      w.fees,
			Frozen:    w.frozen,
		}
	}
	return out
}
