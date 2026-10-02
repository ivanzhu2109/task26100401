package wallet

// fee 按收款金额的 0.1% 向上取整到最小单位。
func fee(amount int64) int64 {
	return (amount + 999) / 1000
}

// Transfer 从 sender 预占全部本金及手续费，预占成功后调用 verifyPIN 复核。
// 复核通过则原子提交整个批次；拒绝或被冻结撤销则仅释放属于本次的预占。
func (s *System) Transfer(sender WalletID, recipients []Recipient, verifyPIN func() bool) Result {
	sw, ok := s.wallets[sender]
	if !ok {
		return Rejected
	}

	var total int64
	for _, r := range recipients {
		total += r.Amount + fee(r.Amount)
	}

	pt := &pendingTransfer{total: total}

	// 预占：只锁付款钱包。已冻结或可用余额不足时直接拒绝，不调用复核器。
	sw.mu.Lock()
	if sw.frozen || sw.balance-sw.reserved < total {
		sw.mu.Unlock()
		return Rejected
	}
	sw.reserved += total
	sw.pending[pt] = struct{}{}
	sw.mu.Unlock()

	// 复核：不持有任何锁，可能长时间阻塞；期间冻结可立即撤销本次预占。
	approved := verifyPIN()

	// 收尾：按统一顺序锁住全部相关钱包，与 SetFrozen/其它提交/Snapshot 互斥。
	ids := make([]WalletID, 0, len(recipients)+1)
	ids = append(ids, sender)
	for _, r := range recipients {
		ids = append(ids, r.To)
	}
	states := s.lockAll(ids)
	defer unlockAll(states)

	if _, alive := sw.pending[pt]; !alive {
		// 已被 SetFrozen 撤销，预占已释放，不得再改变任何状态。
		return Revoked
	}
	delete(sw.pending, pt)
	sw.reserved -= total
	if !approved {
		return Rejected
	}

	// 原子提交：一次性扣款、入账并累计手续费。
	sw.balance -= total
	for _, r := range recipients {
		if rw, ok := s.wallets[r.To]; ok {
			rw.balance += r.Amount
		}
		sw.fees += fee(r.Amount)
	}
	return Success
}
