package repo

import (
	"agentflow/internal/domain"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"

	"gorm.io/gorm"
)

// Local accounts are an optional identity adapter. User IDs, permissions and
// API tokens continue to use the upstream UsersRepo and TokensRepo.
type localAccountRow struct {
	UserID       int64  `gorm:"primaryKey;autoIncrement:false"`
	PasswordHash string `gorm:"size:128;not null"`
}

func (localAccountRow) TableName() string { return "local_accounts" }

// bcrypt only uses the first 72 bytes.
const (
	MinPasswordLength = 6
	MaxPasswordLength = 72
)

var errPasswordLength = fmt.Errorf("password must contain %d–%d bytes", MinPasswordLength, MaxPasswordLength)

func (r *UsersRepo) SetLocalPassword(userID int64, password string) error {
	if _, ok := r.User(userID); !ok {
		return errors.New("user not found")
	}
	if len(password) < MinPasswordLength || len(password) > MaxPasswordLength {
		return errPasswordLength
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return upsert(r.DB, "local_accounts", map[string]any{"user_id": userID, "password_hash": string(hash)})
}

var ErrLoginTaken = errors.New("login already exists")

func (r *UsersRepo) LocalAccountCount() int64 {
	var n int64
	r.DB.Model(&localAccountRow{}).Count(&n)
	return n
}

// PlatformAccount is one user as listed under 平台管理 → 成员账号.
type PlatformAccount struct {
	UserID      int64  `json:"userId"`
	LoginName   string `json:"loginName"`
	ChineseName string `json:"chineseName,omitempty"`
	Admin       bool   `json:"admin"`
	// Password is false for users who sign in through GitHub or a gateway.
	Password        bool       `json:"password"`
	CodeBuddyAPIKey bool       `json:"codeBuddyApiKeyConfigured"`
	CreatedAt       *time.Time `json:"createdAt,omitempty"`
}

// ListPlatformAccounts lists every user, oldest first.
func (r *UsersRepo) ListPlatformAccounts() ([]PlatformAccount, error) {
	var rows []struct {
		ID           int64
		LoginName    string
		ChineseName  string
		PlatformRole string
		CreatedAt    *time.Time
		HasPassword  bool
		HasKey       bool
	}
	err := r.DB.Raw(`SELECT u.id, u.login_name, u.chinese_name, u.platform_role, u.created_at,
		(l.user_id IS NOT NULL) AS has_password,
		(c.codebuddy_api_key IS NOT NULL AND c.codebuddy_api_key <> '') AS has_key
		FROM users u
		LEFT JOIN local_accounts l ON l.user_id = u.id
		LEFT JOIN user_configs c ON c.user_id = u.id
		ORDER BY u.id ASC`).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]PlatformAccount, 0, len(rows))
	for _, row := range rows {
		out = append(out, PlatformAccount{
			UserID: row.ID, LoginName: row.LoginName, ChineseName: row.ChineseName,
			Admin:    IsAllowlistedPlatformAdmin(row.LoginName) || domain.IsPlatformAdmin(domain.User{PlatformRole: row.PlatformRole}),
			Password: row.HasPassword, CodeBuddyAPIKey: row.HasKey, CreatedAt: row.CreatedAt,
		})
	}
	return out, nil
}

// CreateLocalAccount registers a new password account; it never takes over an
// existing user row.
func (r *UsersRepo) CreateLocalAccount(login, password string, admin bool) (domain.User, error) {
	hash, err := localPasswordHash(password)
	if err != nil {
		return domain.User{}, err
	}
	var user domain.User
	err = r.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		user, err = r.createLocalAccount(tx, login, login, hash, admin)
		return err
	})
	return user, err
}

func localPasswordHash(password string) (string, error) {
	if len(password) < MinPasswordLength || len(password) > MaxPasswordLength {
		return "", errPasswordLength
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(hash), err
}

func (r *UsersRepo) createLocalAccount(tx *gorm.DB, login, name, hash string, admin bool) (domain.User, error) {
	now := time.Now().UTC()
	row := userRow{LoginName: login, ChineseName: name, Created: &now, Updated: &now}
	if admin || IsAllowlistedPlatformAdmin(login) {
		row.PlatformRole = domain.PlatformRoleAdmin
	}
	query := tx.Where("login_name = ?", login)
	if !r.isMySQL() {
		query = tx.Where("LOWER(login_name) = LOWER(?)", login)
	}
	var existing userRow
	if err := query.Select("id").Take(&existing).Error; err == nil {
		return domain.User{}, ErrLoginTaken
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.User{}, err
	}
	if err := tx.Create(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return domain.User{}, ErrLoginTaken
		}
		return domain.User{}, err
	}
	if err := tx.Create(&localAccountRow{UserID: row.ID, PasswordHash: hash}).Error; err != nil {
		return domain.User{}, err
	}
	return domain.User{ID: row.ID, LoginName: login, ChineseName: name, PlatformRole: row.PlatformRole, CreatedAt: now, UpdatedAt: now}, nil
}

// Always perform bcrypt work, including for unknown users, to avoid exposing
// the account list through response timing.
var dummyPasswordHash, _ = bcrypt.GenerateFromPassword([]byte("unused-account-password"), bcrypt.DefaultCost)

func (r *UsersRepo) CheckLocalPassword(userID int64, password string) bool {
	var row localAccountRow
	err := r.DB.Where("user_id = ?", userID).First(&row).Error
	hash := []byte(row.PasswordHash)
	if err != nil {
		hash = dummyPasswordHash
	}
	valid := bcrypt.CompareHashAndPassword(hash, []byte(password)) == nil
	return err == nil && valid
}
