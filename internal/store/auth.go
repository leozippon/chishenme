package store

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	hashScheme     = "pbkdf2-sha256"
	hashIterations = 600000
	hashKeyLength  = 32
	hashSaltLength = 16

	minPasswordLength = 8
)

// Admin is the first account, created when the database has no users yet.
type Admin struct {
	Username string
	Password string
}

// User is the person signed in. The password hash never leaves this package.
type User struct {
	ID       int64
	Username string

	tokenVersion int
}

// TokenVersion changes whenever the password changes, which retires old sessions.
func (u User) TokenVersion() int { return u.tokenVersion }

// Authenticate checks the single household login. An unknown name and a wrong
// password report the same error.
func (s *Store) Authenticate(username, password string) (User, error) {
	var (
		u    User
		hash string
	)
	err := s.db.QueryRow(`SELECT id, username, token_version, password_hash FROM users WHERE username = ?`, strings.TrimSpace(username)).
		Scan(&u.ID, &u.Username, &u.tokenVersion, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrBadCredentials
	}
	if err != nil {
		return User{}, fmt.Errorf("read user: %w", err)
	}
	ok, err := verifyPassword(hash, password)
	if err != nil {
		return User{}, err
	}
	if !ok {
		return User{}, ErrBadCredentials
	}
	return u, nil
}

// User reads one account by id.
func (s *Store) User(id int64) (User, error) {
	var u User
	err := s.db.QueryRow(`SELECT id, username, token_version FROM users WHERE id = ?`, id).
		Scan(&u.ID, &u.Username, &u.tokenVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("read user: %w", err)
	}
	return u, nil
}

// ChangePassword verifies the current password before replacing it. Open
// sessions stop working because the token version moves on.
func (s *Store) ChangePassword(id int64, current, next string) error {
	var hash string
	err := s.db.QueryRow(`SELECT password_hash FROM users WHERE id = ?`, id).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read password: %w", err)
	}
	ok, err := verifyPassword(hash, current)
	if err != nil {
		return err
	}
	if !ok {
		return ErrBadCredentials
	}
	if err := checkPassword(next); err != nil {
		return err
	}
	encoded, err := hashPassword(next)
	if err != nil {
		return err
	}
	result, err := s.db.Exec(`UPDATE users SET password_hash = ?, token_version = token_version + 1 WHERE id = ?`, encoded, id)
	if err != nil {
		return fmt.Errorf("set password: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ensureAdmin(admin Admin) error {
	var id int64
	err := s.db.QueryRow(`SELECT id FROM users ORDER BY id LIMIT 1`).Scan(&id)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read users: %w", err)
	}
	if strings.TrimSpace(admin.Password) == "" {
		return errors.New("the database has no account yet: set CHISHENME_ADMIN_PASSWORD")
	}
	username, err := checkUsername(admin.Username)
	if err != nil {
		return fmt.Errorf("administrator username: %w", err)
	}
	if err := checkPassword(admin.Password); err != nil {
		return fmt.Errorf("administrator password: %w", err)
	}
	hash, err := hashPassword(admin.Password)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(
		`INSERT INTO users (username, password_hash, token_version, created_at) VALUES (?, ?, 1, ?)`,
		username, hash, now()); err != nil {
		return fmt.Errorf("create administrator: %w", err)
	}
	s.createdAdmin = username
	return nil
}

func checkUsername(name string) (string, error) {
	name = strings.TrimSpace(name)
	length := utf8.RuneCountInString(name)
	if length < 2 || length > 20 {
		return "", invalid("用户名需要 2 到 20 个字符")
	}
	for _, r := range name {
		if unicode.IsSpace(r) || !unicode.IsPrint(r) || r == '"' {
			return "", invalid("用户名不能包含空格、引号或不可见字符")
		}
	}
	return name, nil
}

func checkPassword(password string) error {
	if utf8.RuneCountInString(password) < minPasswordLength {
		return invalid("密码至少需要 %d 个字符", minPasswordLength)
	}
	if len(password) > 256 {
		return invalid("密码过长")
	}
	return nil
}

var b64 = base64.RawStdEncoding

func hashPassword(password string) (string, error) {
	salt := make([]byte, hashSaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, hashIterations, hashKeyLength)
	if err != nil {
		return "", fmt.Errorf("derive password hash: %w", err)
	}
	return fmt.Sprintf("%s$%d$%s$%s", hashScheme, hashIterations, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

func verifyPassword(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != hashScheme {
		return false, fmt.Errorf("stored password hash is not in %s form", hashScheme)
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations < 1 {
		return false, fmt.Errorf("stored password hash has an invalid iteration count")
	}
	salt, err := b64.DecodeString(parts[2])
	if err != nil {
		return false, fmt.Errorf("stored password hash has an invalid salt: %w", err)
	}
	want, err := b64.DecodeString(parts[3])
	if err != nil {
		return false, fmt.Errorf("stored password hash is unreadable: %w", err)
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iterations, len(want))
	if err != nil {
		return false, fmt.Errorf("derive password hash: %w", err)
	}
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
