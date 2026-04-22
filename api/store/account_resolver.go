package store

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrAccountIdentifierNotFound = errors.New("account identifier not found")

func normalizeAccountIdentifier(identifier string) string {
	v := strings.TrimSpace(identifier)
	v = strings.Trim(v, `"'`)
	if i := strings.IndexAny(v, "=:："); i > -1 {
		left := strings.ToLower(strings.TrimSpace(v[:i]))
		switch left {
		case "api_key", "apikey", "api key", "key", "username", "user", "uid", "id":
			v = strings.TrimSpace(v[i+1:])
		}
	}
	return strings.Trim(strings.TrimSpace(v), `"'`)
}

// ResolveAccountIdentifier 将管理员输入的 API Key / 用户名 / UUID 解析为账户 ID。
//
// 为了兼容旧后台，UUID 可直接使用；API Key 精确匹配；用户名大小写不敏感匹配。
func (s *Store) ResolveAccountIdentifier(ctx context.Context, identifier string) (uuid.UUID, error) {
	identifier = normalizeAccountIdentifier(identifier)
	if identifier == "" {
		return uuid.Nil, ErrAccountIdentifierNotFound
	}
	if id, err := uuid.Parse(identifier); err == nil {
		var exists bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts WHERE id = $1)`, id).Scan(&exists); err != nil {
			return uuid.Nil, err
		}
		if exists {
			return id, nil
		}
		return uuid.Nil, ErrAccountIdentifierNotFound
	}

	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
		SELECT id
		FROM accounts
		WHERE api_key = $1 OR LOWER(username) = LOWER($1)
		ORDER BY CASE WHEN api_key = $1 THEN 0 ELSE 1 END
		LIMIT 1`,
		identifier,
	).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, ErrAccountIdentifierNotFound
		}
		return uuid.Nil, err
	}
	return id, nil
}
