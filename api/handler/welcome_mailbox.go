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

// globalMailboxTTLMinutes reads the system-wide default TTL from app_settings.
func globalMailboxTTLMinutes(ctx context.Context, s *store.Store) int {
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

// effectiveMailboxTTL returns the TTL to use for a specific account:
// account-level TTL takes precedence over the global default.
func effectiveMailboxTTL(ctx context.Context, s *store.Store, accountTTL *int) int {
	if accountTTL != nil {
		return *accountTTL
	}
	return globalMailboxTTLMinutes(ctx, s)
}

// mailboxTTLMinutesFromStore kept for backward compatibility.
func mailboxTTLMinutesFromStore(ctx context.Context, s *store.Store) int {
	return globalMailboxTTLMinutes(ctx, s)
}

// TryCreateMailboxForDomain 在指定已激活域名下为新账户创建邮箱：本地部分随机生成；若 full_address 冲突则换随机串重试。
func TryCreateMailboxForDomain(ctx context.Context, s *store.Store, accountID uuid.UUID, dom *model.Domain) (*model.Mailbox, error) {
	ttl := globalMailboxTTLMinutes(ctx, s)
	for attempts := 0; attempts < 24; attempts++ {
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
	return nil, fmt.Errorf("could not allocate unique mailbox address under domain %s", dom.Domain)
}
