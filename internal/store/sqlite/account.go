package sqlite

import (
	"context"
	"time"

	"github.com/sovannroth/mynotes/internal/model"
)

// ---- users ----

// CountUsers implements store.Users.
func (d *DB) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := d.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// CreateUser implements store.Users.
func (d *DB) CreateUser(ctx context.Context, u *model.User) error {
	res, err := d.q.ExecContext(ctx, `INSERT INTO users(username, password_hash, created_at) VALUES (?,?,?)`,
		u.Username, u.PasswordHash, ft(u.CreatedAt))
	if err != nil {
		return err
	}
	u.ID, err = res.LastInsertId()
	return err
}

func (d *DB) getUser(ctx context.Context, where string, arg any) (*model.User, error) {
	u := &model.User{}
	err := d.q.QueryRowContext(ctx, `SELECT id, username, password_hash, created_at FROM users WHERE `+where, arg).
		Scan(&u.ID, &u.Username, &u.PasswordHash, ts(&u.CreatedAt))
	if err != nil {
		return nil, notFound(err)
	}
	return u, nil
}

// GetUserByUsername implements store.Users.
func (d *DB) GetUserByUsername(ctx context.Context, username string) (*model.User, error) {
	return d.getUser(ctx, "username = ?", username)
}

// GetUser implements store.Users.
func (d *DB) GetUser(ctx context.Context, id int64) (*model.User, error) {
	return d.getUser(ctx, "id = ?", id)
}

// UpdatePassword implements store.Users.
func (d *DB) UpdatePassword(ctx context.Context, id int64, hash string) error {
	return checkAffected(d.q.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, hash, id))
}

// ---- settings ----

// GetSettings implements store.Settings.
func (d *DB) GetSettings(ctx context.Context) (map[string]string, error) {
	rows, err := d.q.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		m[k] = v
	}
	return m, rows.Err()
}

// SetSetting implements store.Settings.
func (d *DB) SetSetting(ctx context.Context, key, value string) error {
	_, err := d.q.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// DeleteSetting implements store.Settings.
func (d *DB) DeleteSetting(ctx context.Context, key string) error {
	_, err := d.q.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, key)
	return err
}

// ---- sessions ----

// CreateSession implements store.Sessions.
func (d *DB) CreateSession(ctx context.Context, s *model.Session) error {
	_, err := d.q.ExecContext(ctx, `INSERT INTO sessions(id_hash, user_id, csrf_token, created_at, last_seen_at) VALUES (?,?,?,?,?)`,
		s.IDHash, s.UserID, s.CSRFToken, ft(s.CreatedAt), ft(s.LastSeenAt))
	return err
}

// GetSession implements store.Sessions.
func (d *DB) GetSession(ctx context.Context, idHash string) (*model.Session, error) {
	s := &model.Session{}
	err := d.q.QueryRowContext(ctx, `SELECT id_hash, user_id, csrf_token, created_at, last_seen_at FROM sessions WHERE id_hash = ?`, idHash).
		Scan(&s.IDHash, &s.UserID, &s.CSRFToken, ts(&s.CreatedAt), ts(&s.LastSeenAt))
	if err != nil {
		return nil, notFound(err)
	}
	return s, nil
}

// TouchSession implements store.Sessions.
func (d *DB) TouchSession(ctx context.Context, idHash string, at time.Time) error {
	_, err := d.q.ExecContext(ctx, `UPDATE sessions SET last_seen_at = ? WHERE id_hash = ?`, ft(at), idHash)
	return err
}

// DeleteSession implements store.Sessions.
func (d *DB) DeleteSession(ctx context.Context, idHash string) error {
	_, err := d.q.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash = ?`, idHash)
	return err
}

// DeleteSessionsExcept implements store.Sessions.
func (d *DB) DeleteSessionsExcept(ctx context.Context, keep string) error {
	_, err := d.q.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash <> ?`, keep)
	return err
}

// DeleteSessionsIdleSince implements store.Sessions.
func (d *DB) DeleteSessionsIdleSince(ctx context.Context, before time.Time) error {
	_, err := d.q.ExecContext(ctx, `DELETE FROM sessions WHERE last_seen_at < ?`, ft(before))
	return err
}

// ---- api tokens ----

// CreateToken implements store.Tokens.
func (d *DB) CreateToken(ctx context.Context, t *model.APIToken) error {
	res, err := d.q.ExecContext(ctx, `INSERT INTO api_tokens(name, token_hash, created_at) VALUES (?,?,?)`, t.Name, t.TokenHash, ft(t.CreatedAt))
	if err != nil {
		return err
	}
	t.ID, err = res.LastInsertId()
	return err
}

const tokenCols = `id, name, token_hash, last_used_at, created_at, revoked_at`

func scanToken(sc interface{ Scan(...any) error }) (*model.APIToken, error) {
	t := &model.APIToken{}
	err := sc.Scan(&t.ID, &t.Name, &t.TokenHash, tsp(&t.LastUsedAt), ts(&t.CreatedAt), tsp(&t.RevokedAt))
	return t, err
}

// ListTokens implements store.Tokens.
func (d *DB) ListTokens(ctx context.Context) ([]model.APIToken, error) {
	rows, err := d.q.QueryContext(ctx, `SELECT `+tokenCols+` FROM api_tokens ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.APIToken
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// GetTokenByHash implements store.Tokens.
func (d *DB) GetTokenByHash(ctx context.Context, hash string) (*model.APIToken, error) {
	t, err := scanToken(d.q.QueryRowContext(ctx, `SELECT `+tokenCols+` FROM api_tokens WHERE token_hash = ?`, hash))
	if err != nil {
		return nil, notFound(err)
	}
	return t, nil
}

// TouchToken implements store.Tokens.
func (d *DB) TouchToken(ctx context.Context, id int64, at time.Time) error {
	_, err := d.q.ExecContext(ctx, `UPDATE api_tokens SET last_used_at = ? WHERE id = ?`, ft(at), id)
	return err
}

// RevokeToken implements store.Tokens.
func (d *DB) RevokeToken(ctx context.Context, id int64, at time.Time) error {
	return checkAffected(d.q.ExecContext(ctx, `UPDATE api_tokens SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, ft(at), id))
}
