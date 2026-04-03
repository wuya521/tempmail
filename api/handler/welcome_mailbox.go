package handler

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"tempmail/model"
	"tempmail/store"

	"github.com/google/uuid"
)

// mailboxTTLMinutesFromStore 与 POST /api/mailboxes 一致：0=永不过期，>0=分钟，否则默认 30
func mailboxTTLMinutesFromStore(ctx context.Context, s *store.Store) int {
	ttlMinutes := 30
	if ttlStr, err := s.GetSetting(ctx, "mailbox_ttl_minutes"); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(ttlStr)); err == nil {
			if n == 0 {
				ttlMinutes = 0
			} else if n > 0 {
				ttlMinutes = n
			}
		}
	}
	return ttlMinutes
}

// TryCreateWelcomeMailbox 为新账户自动创建首个邮箱（随机本地部分 + 随机激活域名）。
// 若无激活域名或地址冲突耗尽重试，返回 nil 与错误。
func TryCreateWelcomeMailbox(ctx context.Context, s *store.Store, accountID uuid.UUID) (*model.Mailbox, error) {
	dom, err := s.GetRandomActiveDomain(ctx)
	if err != nil {
		return nil, err
	}
	ttl := mailboxTTLMinutesFromStore(ctx, s)
	for attempts := 0; attempts < 8; attempts++ {
		address := strings.ToLower(store.GenerateRandomAddress())
		full := fmt.Sprintf("%s@%s", address, dom.Domain)
		mb, err := s.CreateMailbox(ctx, accountID, address, dom.ID, full, ttl)
		if err == nil {
			return mb, nil
		}
		if !strings.Contains(err.Error(), "duplicate") && !strings.Contains(err.Error(), "unique") {
			return nil, err
		}
	}
	return nil, fmt.Errorf("could not allocate unique mailbox address")
}
