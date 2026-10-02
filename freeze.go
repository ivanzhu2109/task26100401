package usdtwallet

// SetFrozen 设置钱包冻结状态。冻结只禁止付款，不禁止收款。
//
// 设为冻结时，在钱包锁内原子地撤销该钱包全部尚未提交的付款
// 并释放其预占，无需等待复核器返回；解冻只允许新转账，不恢复旧转账。
func (s *System) SetFrozen(wallet string, frozen bool) {
	w := s.wallets[wallet]
	w.mu.Lock()
	defer w.mu.Unlock()
	w.frozen = frozen
	if !frozen {
		return
	}
	for t := range w.pending {
		delete(w.pending, t)
		w.held -= t.total
	}
}
