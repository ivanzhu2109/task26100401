package usdtwallet

// Transfer 从 sender 的可用余额中预占全部本金及手续费，
// 预占成功后调用 verifyPIN 复核，通过后原子提交整个批次。
//
// 预占只锁付款钱包；复核期间不持有任何锁；
// 提交时按钱包 ID 字典序锁定全部相关钱包，原子完成整批记账。
func (s *System) Transfer(sender string, recipients map[string]int64, verifyPIN VerifyPIN) Result {
	snd := s.wallets[sender]

	t := &transfer{sender: snd, senderID: sender}
	for id, amt := range recipients {
		t.recipIDs = append(t.recipIDs, id)
		t.recips = append(t.recips, s.wallets[id])
		t.amounts = append(t.amounts, amt)
		t.total += amt + fee(amt)
	}

	// 预占：付款钱包已冻结或可用余额不足时直接拒绝，不调用复核器。
	snd.mu.Lock()
	if snd.frozen || snd.balance-snd.held < t.total {
		snd.mu.Unlock()
		return Rejected
	}
	snd.held += t.total
	snd.pending[t] = struct{}{}
	snd.mu.Unlock()

	// 复核：不持有任何锁，可能长时间阻塞。
	// 阻塞期间冻结操作可独立撤销本次预占。
	approved := verifyPIN()

	if !approved {
		// 正常拒绝：释放属于本次转账的预占。
		// 若冻结已抢先撤销，则不重复释放，并报告 Revoked。
		snd.mu.Lock()
		_, live := snd.pending[t]
		if live {
			delete(snd.pending, t)
			snd.held -= t.total
		}
		snd.mu.Unlock()
		if !live {
			return Revoked
		}
		return Rejected
	}

	// 提交：按字典序锁定付款与全部收款钱包。
	// 与冻结竞争时，谁先拿到付款钱包的锁谁先生效：
	// 要么完整提交在先，要么冻结撤销在先。
	ids := make([]string, 0, len(t.recipIDs)+1)
	ids = append(ids, t.senderID)
	ids = append(ids, t.recipIDs...)
	_, unlock := s.lockWallets(ids)
	defer unlock()

	if _, live := snd.pending[t]; !live {
		// 已被冻结撤销：预占已由冻结方释放，不得再改变任何状态。
		return Revoked
	}
	delete(snd.pending, t)
	snd.held -= t.total
	snd.balance -= t.total
	for i, w := range t.recips {
		w.balance += t.amounts[i]
		snd.fees += fee(t.amounts[i])
	}
	return Success
}
