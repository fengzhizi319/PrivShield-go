// Package auth — 用户存储与认证管理引擎。
//
// 实现用户全生命周期（注册、口令哈希存储、防爆破锁定、权限变更、状态管控、密钥签发/吊销、
// 登录会话与实时同步）。设计要点：
//
//  1. 口令：bcrypt(cost=12) 加盐存储；**口令哈希必须持久化**（经独立 diskUser DTO 落盘，
//     对外 JSON 结构仍以 `json:"-"` 屏蔽，保证任何响应/日志都不会输出哈希）；
//  2. 凭证：明文 Token 永不落盘，持久化只保存 SHA-256 摘要（HashToken），活密钥索引以摘要为键，
//     由 Settings.LiveInternalHashedKeys 提供给认证内核；
//  3. 会话：登录会话 Token 为内存态（不落盘），进程重启后需重新登录，改密/冻结/注销即时失效；
//  4. 并发：对外一律返回深拷贝，内部变更采用 copy-on-write，杜绝快照持有者读到被并发改写的对象；
//  5. 有界：单用户 API Key 上限、并发会话上限、过期条目惰性清理，避免密钥表无界膨胀
//     （认证为 O(n) 常量时间比对，膨胀会直接放大认证延迟）；
//  6. 自锁防护：删除/冻结/降权「最后一个活跃管理员」一律拒绝。
package auth

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// bcryptCost 为口令哈希工作因子（等保三级 G-04 要求加盐强哈希；12 约 200~300ms/次）。
const bcryptCost = 12

// diskFormatVersion 为持久化文件格式版本，便于后续无损迁移。
const diskFormatVersion = 2

type activeKeyEntry struct {
	Username string
	KeyID    string
	// Config 为只读快照：权限/状态变更时**整体替换**该指针（copy-on-write），
	// 不得原地改写，否则已下发的快照会在无锁读取时被并发改写（数据竞争）。
	Config *KeyConfig
	Active bool
}

// sessionEntry 表示一次登录会话（内存态，不持久化）。
type sessionEntry struct {
	Username  string
	Scopes    []string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// UserStore 管理用户账号、动态 API Key 与登录会话。
type UserStore struct {
	mu             sync.RWMutex
	filePath       string
	users          map[string]*User
	activeKeys     map[string]*activeKeyEntry // HashToken(token) -> entry
	sessions       map[string]*sessionEntry   // HashToken(token) -> session（不落盘）
	failedAttempts map[string]int             // username -> consecutive failures
	lockedUntil    map[string]time.Time       // username -> lockout expiration
	logger         *slog.Logger
	version        versionCounter // 任何影响活密钥的变更都会递增

	// snapMu 保护快照缓存：LiveHashedKeys 仅在 version 变化时重建，其余请求零分配复用。
	snapMu         sync.Mutex
	snapshot       map[string]*KeyConfig
	snapshotVer    uint64
	snapshotInit   bool
	dummyHashOnce  sync.Once
	dummyHashValue string
	migrated       bool
}

// diskUser 是持久化 DTO：显式包含口令哈希与口令历史（bcrypt 密文可安全落盘），
// 从而修复「User.PasswordHash 标注 json:"-" 导致重启后无人能登录」的缺陷；
// 同时保证对外 API 结构体永不序列化口令材料。
type diskUser struct {
	Username          string                   `json:"username"`
	DisplayName       string                   `json:"display_name"`
	Role              string                   `json:"role"`
	Scopes            []string                 `json:"scopes"`
	Status            UserStatus               `json:"status"`
	PasswordHash      string                   `json:"password_hash"`
	PasswordHistory   []string                 `json:"password_history,omitempty"`
	PasswordUpdatedAt time.Time                `json:"password_updated_at"`
	APIKeys           map[string]*APIKeyRecord `json:"api_keys,omitempty"`
	CreatedAt         time.Time                `json:"created_at"`
	UpdatedAt         time.Time                `json:"updated_at"`
}

// diskData 用于持久化到磁盘的顶层 JSON 结构。
type diskData struct {
	Version int                  `json:"version"`
	Users   map[string]*diskUser `json:"users"`
}

func toDiskUser(u *User) *diskUser {
	d := &diskUser{
		Username:          u.Username,
		DisplayName:       u.DisplayName,
		Role:              u.Role,
		Scopes:            append([]string(nil), u.Scopes...),
		Status:            u.Status,
		PasswordHash:      u.PasswordHash,
		PasswordHistory:   append([]string(nil), u.PasswordHistory...),
		PasswordUpdatedAt: u.PasswordUpdatedAt,
		CreatedAt:         u.CreatedAt,
		UpdatedAt:         u.UpdatedAt,
	}
	if len(u.APIKeys) > 0 {
		d.APIKeys = make(map[string]*APIKeyRecord, len(u.APIKeys))
		for id, k := range u.APIKeys {
			rec := *k
			rec.LegacyToken = "" // 明文一律不落盘
			rec.Scopes = append([]string(nil), k.Scopes...)
			d.APIKeys[id] = &rec
		}
	}
	return d
}

func fromDiskUser(d *diskUser) *User {
	u := &User{
		Username:          d.Username,
		DisplayName:       d.DisplayName,
		Role:              d.Role,
		Scopes:            append([]string(nil), d.Scopes...),
		Status:            d.Status,
		PasswordHash:      d.PasswordHash,
		PasswordHistory:   append([]string(nil), d.PasswordHistory...),
		PasswordUpdatedAt: d.PasswordUpdatedAt,
		APIKeys:           make(map[string]*APIKeyRecord, len(d.APIKeys)),
		CreatedAt:         d.CreatedAt,
		UpdatedAt:         d.UpdatedAt,
	}
	for id, k := range d.APIKeys {
		rec := *k
		rec.Scopes = append([]string(nil), k.Scopes...)
		u.APIKeys[id] = &rec
	}
	return u
}

// NewUserStore 创建用户存储实例。如果 filePath 非空且文件存在，则从磁盘加载。
func NewUserStore(filePath string) (*UserStore, error) {
	s := &UserStore{
		filePath:       filePath,
		users:          make(map[string]*User),
		activeKeys:     make(map[string]*activeKeyEntry),
		sessions:       make(map[string]*sessionEntry),
		failedAttempts: make(map[string]int),
		lockedUntil:    make(map[string]time.Time),
		logger:         slog.Default(),
	}

	if filePath != "" {
		if err := s.loadFromFile(); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("auth: failed to load user store from %s: %w", filePath, err)
		}
	}
	return s, nil
}

// SetAuditLogger 设置审计日志输出器（默认 slog.Default()）。
func (s *UserStore) SetAuditLogger(logger *slog.Logger) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if logger == nil {
		logger = slog.Default()
	}
	s.logger = logger
}

// Version 实现 LiveKeySource/HashedLiveKeySource：返回活密钥变更版本号。
func (s *UserStore) Version() uint64 {
	if s == nil {
		return 0
	}
	return s.version.get()
}

// loadFromFile 从磁盘读取用户数据并重建活跃密钥索引（以 Token 摘要为键）。
func (s *UserStore) loadFromFile() error {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}

	var d diskData
	if err := json.Unmarshal(data, &d); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	users := make(map[string]*User, len(d.Users))
	for name, du := range d.Users {
		if du == nil {
			continue
		}
		u := fromDiskUser(du)
		u.Username = NormalizeUsername(name)
		// 状态 fail-closed：无法识别的状态一律视为冻结，避免脏数据放大权限。
		if u.Status != UserStatusActive && u.Status != UserStatusDisabled {
			u.Status = UserStatusDisabled
		}
		for keyID, rec := range u.APIKeys {
			if rec == nil {
				delete(u.APIKeys, keyID)
				continue
			}
			// 兼容 v1 格式（明文 token 落盘）：转为摘要并丢弃明文，下次保存即完成迁移。
			if rec.TokenHash == "" && rec.LegacyToken != "" {
				rec.TokenHash = HashToken(rec.LegacyToken)
				s.migrated = true
			}
			rec.LegacyToken = ""
		}
		users[u.Username] = u
	}
	s.users = users

	// 重建内存 activeKeys 索引（键为 Token 摘要）
	for username, user := range s.users {
		isActive := user.Status == UserStatusActive
		for keyID, keyRec := range user.APIKeys {
			if keyRec.TokenHash == "" || keyRec.IsExpired() {
				continue
			}
			exp := keyRec.ExpiresAt
			s.activeKeys[keyRec.TokenHash] = &activeKeyEntry{
				Username: username,
				KeyID:    keyID,
				Config: &KeyConfig{
					Name:      username + ":" + keyRec.Name,
					Subject:   username,
					Scopes:    append([]string(nil), keyRec.Scopes...),
					ExpiresAt: exp,
				},
				Active: isActive,
			}
		}
	}
	s.version.bump()

	if s.migrated {
		// 迁移后立即回写，确保磁盘上不再残留明文 Token（best-effort，失败仅告警不阻断启动）。
		if err := s.saveToFileLocked(); err != nil {
			s.logger.Warn("UserStore: legacy plaintext token migration persisted with error",
				"path", s.filePath, "error", err.Error())
		} else {
			s.logger.Info("UserStore: migrated legacy plaintext tokens to SHA-256 digests", "path", s.filePath)
		}
	}
	return nil
}

// saveToFileLocked 原子写入用户数据到磁盘文件（目录 0700 / 文件 0600 + fsync + rename）。
func (s *UserStore) saveToFileLocked() error {
	if s.filePath == "" {
		return nil
	}

	dir := filepath.Dir(s.filePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("auth: failed to create directory for user store: %w", err)
	}

	d := diskData{Version: diskFormatVersion, Users: make(map[string]*diskUser, len(s.users))}
	for name, u := range s.users {
		d.Users[name] = toDiskUser(u)
	}
	payload, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return fmt.Errorf("auth: failed to marshal user data: %w", err)
	}

	tmp, err := os.CreateTemp(dir, filepath.Base(s.filePath)+".tmp-*")
	if err != nil {
		return fmt.Errorf("auth: failed to create temp file: %w", err)
	}
	tmpName := tmp.Name()
	renamed := false
	defer func() {
		if !renamed {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(payload); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("auth: failed to write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("auth: failed to sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("auth: failed to close temp file: %w", err)
	}
	if err := os.Rename(tmpName, s.filePath); err != nil {
		return fmt.Errorf("auth: failed to replace user store file: %w", err)
	}
	renamed = true
	return nil
}

// Register 注册新用户。
// 口令满足等保三级 G-04 复杂度与长度上下限，采用 bcrypt (cost=12) 加盐存储。
func (s *UserStore) Register(username, password, displayName, role string, scopes []string) (*User, error) {
	username = NormalizeUsername(username)
	if !IsValidUsername(username) {
		return nil, ErrInvalidUsername
	}
	if err := ValidatePasswordStrength(password, username); err != nil {
		return nil, err
	}
	if err := ValidateDisplayName(displayName); err != nil {
		return nil, err
	}
	if role == "" {
		role = "developer"
	}
	if !IsValidRole(role) {
		return nil, fmt.Errorf("%w: %q (known roles: %v)", ErrInvalidRole, role, KnownRoles)
	}

	normScopes, err := ValidateScopes(scopes)
	if err != nil {
		return nil, err
	}
	finalScopes := normScopes
	if len(finalScopes) == 0 {
		finalScopes = DefaultScopesForRole(role)
	}
	// 防越权：非特权角色不得携带管理类 scope（即使由管理员显式指定也需角色匹配）。
	if !IsPrivilegedRole(role) {
		for _, sc := range finalScopes {
			switch sc {
			case "*", "admin", "user:admin", "ops:admin", "hub:admin":
				return nil, fmt.Errorf("%w: role %q cannot hold management scope %q", ErrForbiddenScope, role, sc)
			}
		}
	}

	// bcrypt 计算在锁外执行（cost=12 约 200ms+），避免注册风暴阻塞全部认证操作。
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return nil, fmt.Errorf("auth: hash password failed: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.users[username]; exists {
		return nil, ErrUserAlreadyExists
	}

	now := time.Now().UTC()
	user := &User{
		Username:          username,
		DisplayName:       displayName,
		Role:              role,
		Scopes:            finalScopes,
		Status:            UserStatusActive,
		PasswordHash:      string(hash),
		PasswordUpdatedAt: now,
		APIKeys:           make(map[string]*APIKeyRecord),
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if user.DisplayName == "" {
		user.DisplayName = username
	}

	s.users[username] = user
	if err := s.saveToFileLocked(); err != nil {
		delete(s.users, username)
		return nil, err
	}
	s.version.bump()

	return user.SanitizedCopy(), nil
}

// Authenticate 校验用户名与密码。
// 三级等保 G-03：连续 5 次错误锁定账号 15 分钟。成功后重置失败计数。
// 用户不存在时同样执行一次 bcrypt 比对（哑哈希），使「用户不存在」与「口令错误」耗时一致，
// 抑制基于时序的账号枚举。
func (s *UserStore) Authenticate(username, password string) (*User, error) {
	username = NormalizeUsername(username)

	s.mu.Lock()
	// 检查锁定状态（顺带清理已过期的锁定记录，防止 map 长期驻留）
	if until, locked := s.lockedUntil[username]; locked {
		if time.Now().Before(until) {
			s.mu.Unlock()
			return nil, ErrAccountLocked
		}
		delete(s.lockedUntil, username)
		delete(s.failedAttempts, username)
	}

	user, exists := s.users[username]
	if !exists {
		s.mu.Unlock()
		_ = bcrypt.CompareHashAndPassword([]byte(s.dummyHash()), []byte(password))
		return nil, ErrUserNotFound
	}
	if user.Status != UserStatusActive {
		s.mu.Unlock()
		return nil, ErrUserDisabled
	}
	hash := user.PasswordHash
	s.mu.Unlock()

	// 口令哈希缺失（历史脏数据）时 fail-closed，绝不放行。
	if hash == "" {
		return nil, ErrInvalidPassword
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		s.recordFailedLogin(username)
		return nil, ErrInvalidPassword
	}

	s.resetFailedLogin(username)

	s.mu.RLock()
	defer s.mu.RUnlock()
	if u, ok := s.users[username]; ok {
		return u.SanitizedCopy(), nil
	}
	return nil, ErrUserNotFound
}

// dummyHash 惰性生成一次性哑 bcrypt 哈希，用于统一「用户不存在」路径的耗时。
func (s *UserStore) dummyHash() string {
	s.dummyHashOnce.Do(func() {
		if token, err := GenerateSecureToken(); err == nil {
			if h, herr := bcrypt.GenerateFromPassword([]byte(token), bcryptCost); herr == nil {
				s.dummyHashValue = string(h)
				return
			}
		}
		// 极端情况下（随机源不可用）退化为固定字符串：比对必然失败，语义仍然安全。
		s.dummyHashValue = "$2a$12$C6UzMDM.H6dfI/f/IKcEeO7ZBpDLfKdYbOoXGwGxXvSdKfPvRr5eW"
	})
	return s.dummyHashValue
}

func (s *UserStore) recordFailedLogin(username string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failedAttempts[username]++
	if s.failedAttempts[username] >= MaxFailedLoginAttempts {
		s.lockedUntil[username] = time.Now().Add(AccountLockoutDuration)
		delete(s.failedAttempts, username)
		s.logger.Warn("auth_audit: account locked after consecutive failed logins",
			"event", "account_locked",
			"target_user", username,
			"max_attempts", MaxFailedLoginAttempts,
			"lockout_minutes", int(AccountLockoutDuration/time.Minute))
	}
}

func (s *UserStore) resetFailedLogin(username string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.failedAttempts, username)
	delete(s.lockedUntil, username)
}

// IsLocked 检查用户是否处于防爆破锁定状态。
func (s *UserStore) IsLocked(username string) bool {
	return s.LockoutRemaining(NormalizeUsername(username)) > 0
}

// LockoutRemaining 返回剩余锁定时长（未锁定返回 0），供 HTTP Retry-After 头使用。
func (s *UserStore) LockoutRemaining(username string) time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	until, locked := s.lockedUntil[NormalizeUsername(username)]
	if !locked {
		return 0
	}
	remaining := time.Until(until)
	if remaining <= 0 {
		return 0
	}
	return remaining
}

// GetUser 获取单个用户详情的脱敏克隆体（不含口令哈希与 Token 明文）。
func (s *UserStore) GetUser(username string) (*User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	user, exists := s.users[NormalizeUsername(username)]
	if !exists {
		return nil, ErrUserNotFound
	}
	return user.SanitizedCopy(), nil
}

// Exists 判断用户是否存在（用于注册引导判定）。
func (s *UserStore) Exists(username string) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.users[NormalizeUsername(username)]
	return ok
}

// ListUsers 获取所有已注册用户的脱敏摘要列表（按创建时间倒序）。
func (s *UserStore) ListUsers() []UserSummary {
	s.mu.RLock()
	defer s.mu.RUnlock()

	list := make([]UserSummary, 0, len(s.users))
	for _, u := range s.users {
		list = append(list, u.ToSummary())
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].CreatedAt.Equal(list[j].CreatedAt) {
			return list[i].Username < list[j].Username
		}
		return list[i].CreatedAt.After(list[j].CreatedAt)
	})
	return list
}

// UpdatePermissions 调整用户角色与权限，并实时联动刷新在线 API Key 与登录会话的 Scope。
func (s *UserStore) UpdatePermissions(username, role string, scopes []string) error {
	username = NormalizeUsername(username)
	if role != "" && !IsValidRole(role) {
		return fmt.Errorf("%w: %q (known roles: %v)", ErrInvalidRole, role, KnownRoles)
	}
	normScopes, err := ValidateScopes(scopes)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	user, exists := s.users[username]
	if !exists {
		return ErrUserNotFound
	}

	nextRole := user.Role
	if role != "" {
		nextRole = role
	}
	nextScopes := user.Scopes
	if len(normScopes) > 0 {
		nextScopes = normScopes
	} else if role != "" {
		nextScopes = DefaultScopesForRole(role)
	}

	// 自锁防护：若本次变更会摘掉最后一个活跃管理员的管理能力，直接拒绝。
	if HasAdminCapability(user) {
		probe := &User{Username: username, Status: user.Status, Role: nextRole, Scopes: nextScopes}
		if !HasAdminCapability(probe) {
			if err := s.ensureNotLastAdminLocked(user); err != nil {
				return err
			}
		}
	}

	user.Role = nextRole
	user.Scopes = append([]string(nil), nextScopes...)
	user.UpdatedAt = time.Now().UTC()

	// 毫秒级联动：以 copy-on-write 方式替换活跃 API Key 配置（不得原地改写共享指针）
	for hash, entry := range s.activeKeys {
		if entry.Username != username {
			continue
		}
		updated := entry.Config.Clone()
		updated.Scopes = append([]string(nil), user.Scopes...)
		s.activeKeys[hash] = &activeKeyEntry{Username: entry.Username, KeyID: entry.KeyID, Config: updated, Active: entry.Active}
	}
	// 登录会话同步刷新（会话 Scope 为用户当前权限的快照）
	for _, sess := range s.sessions {
		if sess.Username == username {
			sess.Scopes = append([]string(nil), user.Scopes...)
		}
	}

	if err := s.saveToFileLocked(); err != nil {
		return err
	}
	s.version.bump()
	return nil
}

// SetStatus 调整用户状态（active / disabled）。
// 若禁用：名下所有活跃 API Key 立即失效，且全部登录会话被强制下线；若恢复启用：API Key 自动生效
// （会话不恢复，需重新登录）。
func (s *UserStore) SetStatus(username string, status UserStatus) error {
	username = NormalizeUsername(username)
	if status != UserStatusActive && status != UserStatusDisabled {
		return ErrInvalidStatus
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	user, exists := s.users[username]
	if !exists {
		return ErrUserNotFound
	}
	if status == UserStatusDisabled {
		if err := s.ensureNotLastAdminLocked(user); err != nil {
			return err
		}
	}

	user.Status = status
	user.UpdatedAt = time.Now().UTC()

	isActive := status == UserStatusActive
	for _, entry := range s.activeKeys {
		if entry.Username == username {
			entry.Active = isActive
		}
	}
	if !isActive {
		s.revokeSessionsLocked(username)
	}

	if err := s.saveToFileLocked(); err != nil {
		return err
	}
	s.version.bump()
	return nil
}

// DeleteUser 注销并删除用户，物理吊销其全部 API Key 与登录会话。
func (s *UserStore) DeleteUser(username string) error {
	username = NormalizeUsername(username)

	s.mu.Lock()
	defer s.mu.Unlock()

	user, exists := s.users[username]
	if !exists {
		return ErrUserNotFound
	}
	if err := s.ensureNotLastAdminLocked(user); err != nil {
		return err
	}

	// 清理活跃密钥表与会话
	for hash, entry := range s.activeKeys {
		if entry.Username == username {
			delete(s.activeKeys, hash)
		}
	}
	s.revokeSessionsLocked(username)

	delete(s.users, username)
	delete(s.failedAttempts, username)
	delete(s.lockedUntil, username)

	if err := s.saveToFileLocked(); err != nil {
		return err
	}
	s.version.bump()
	return nil
}

// ensureNotLastAdminLocked 拒绝「移除最后一个活跃管理员」的操作，防止管理面自锁。
func (s *UserStore) ensureNotLastAdminLocked(target *User) error {
	if !HasAdminCapability(target) {
		return nil
	}
	for name, u := range s.users {
		if name == target.Username {
			continue
		}
		if HasAdminCapability(u) {
			return nil
		}
	}
	return ErrLastAdmin
}

// IssueAPIKey 为指定用户动态签发新的 API Key。
// 返回创建的记录（不含明文）与完整明文 Token；明文仅在此处返回一次，落盘只保存 SHA-256 摘要。
// ttl 语义：0 → 默认 DefaultAPIKeyTTL（30 天）；负值或超过 MaxAPIKeyTTL（90 天）→ ErrInvalidTTL。
func (s *UserStore) IssueAPIKey(username, keyName string, scopes []string, ttl time.Duration) (*APIKeyRecord, string, error) {
	username = NormalizeUsername(username)
	if keyName != "" {
		if err := ValidateKeyName(keyName); err != nil {
			return nil, "", err
		}
	}
	normScopes, err := ValidateScopes(scopes)
	if err != nil {
		return nil, "", err
	}
	if ttl < 0 || ttl > MaxAPIKeyTTL {
		return nil, "", ErrInvalidTTL
	}
	if ttl == 0 {
		ttl = DefaultAPIKeyTTL
	}

	token, err := GenerateSecureToken()
	if err != nil {
		return nil, "", fmt.Errorf("auth: generate token failed: %w", err)
	}
	keyID, err := GenerateKeyID()
	if err != nil {
		return nil, "", fmt.Errorf("auth: generate key id failed: %w", err)
	}
	tokenHash := HashToken(token)

	s.mu.Lock()
	defer s.mu.Unlock()

	user, exists := s.users[username]
	if !exists {
		return nil, "", ErrUserNotFound
	}
	if user.Status != UserStatusActive {
		return nil, "", ErrUserDisabled
	}

	// 惰性清理：先回收该用户已过期 Key，再做配额判定（避免过期条目挤占额度）。
	s.pruneExpiredKeysLocked(username)
	if len(user.APIKeys) >= MaxAPIKeysPerUser {
		return nil, "", fmt.Errorf("%w: max %d active keys per user", ErrTooManyKeys, MaxAPIKeysPerUser)
	}

	// 校验 requested scopes 不得越权（必须是用户自身权限的子集）
	finalScopes := normScopes
	if len(finalScopes) == 0 {
		finalScopes = append([]string(nil), user.Scopes...)
	} else if !userHoldsWildcard(user) {
		userScopeMap := make(map[string]bool, len(user.Scopes))
		for _, us := range user.Scopes {
			userScopeMap[us] = true
		}
		for _, reqS := range finalScopes {
			if !userScopeMap[reqS] {
				return nil, "", fmt.Errorf("%w: user does not hold scope %q", ErrForbiddenScope, reqS)
			}
		}
	}

	if keyName == "" {
		keyName = fmt.Sprintf("key-%d", len(user.APIKeys)+1)
	}

	now := time.Now().UTC()
	exp := now.Add(ttl)
	record := &APIKeyRecord{
		KeyID:       keyID,
		Name:        keyName,
		TokenHash:   tokenHash,
		TokenPrefix: tokenPrefix(token),
		Scopes:      append([]string(nil), finalScopes...),
		CreatedAt:   now,
		ExpiresAt:   &exp,
	}

	if user.APIKeys == nil {
		user.APIKeys = make(map[string]*APIKeyRecord)
	}
	user.APIKeys[keyID] = record
	user.UpdatedAt = now

	// 实时装载到 activeKeys（键为 Token 摘要）
	s.activeKeys[tokenHash] = &activeKeyEntry{
		Username: username,
		KeyID:    keyID,
		Config: &KeyConfig{
			Name:      username + ":" + keyName,
			Subject:   username,
			Scopes:    append([]string(nil), finalScopes...),
			ExpiresAt: &exp,
		},
		Active: true,
	}

	if err := s.saveToFileLocked(); err != nil {
		delete(user.APIKeys, keyID)
		delete(s.activeKeys, tokenHash)
		return nil, "", err
	}
	s.version.bump()

	out := *record
	out.Scopes = append([]string(nil), record.Scopes...)
	return &out, token, nil
}

func userHoldsWildcard(u *User) bool {
	for _, us := range u.Scopes {
		if us == "*" || us == "admin" {
			return true
		}
	}
	return false
}

// pruneExpiredKeysLocked 清理指定用户已过期的 Key（记录 + 活密钥索引）。
func (s *UserStore) pruneExpiredKeysLocked(username string) {
	user, ok := s.users[username]
	if !ok {
		return
	}
	for keyID, rec := range user.APIKeys {
		if rec == nil || !rec.IsExpired() {
			continue
		}
		delete(user.APIKeys, keyID)
		if rec.TokenHash != "" {
			delete(s.activeKeys, rec.TokenHash)
		}
	}
}

// RevokeAPIKey 吊销用户的指定 API Key。
func (s *UserStore) RevokeAPIKey(username, keyID string) error {
	username = NormalizeUsername(username)

	s.mu.Lock()
	defer s.mu.Unlock()

	user, exists := s.users[username]
	if !exists {
		return ErrUserNotFound
	}
	rec, exists := user.APIKeys[keyID]
	if !exists {
		return ErrKeyNotFound
	}

	if rec.TokenHash != "" {
		delete(s.activeKeys, rec.TokenHash)
	}
	delete(user.APIKeys, keyID)
	user.UpdatedAt = time.Now().UTC()

	if err := s.saveToFileLocked(); err != nil {
		return err
	}
	s.version.bump()
	return nil
}

// ListAPIKeys 返回用户的全部 API Key 清单（仅含摘要与前缀，绝无明文）。
func (s *UserStore) ListAPIKeys(username string) ([]APIKeyRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	user, exists := s.users[NormalizeUsername(username)]
	if !exists {
		return nil, ErrUserNotFound
	}

	list := make([]APIKeyRecord, 0, len(user.APIKeys))
	for _, rec := range user.APIKeys {
		r := *rec
		r.LegacyToken = ""
		r.Scopes = append([]string(nil), rec.Scopes...)
		list = append(list, r)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].CreatedAt.Equal(list[j].CreatedAt) {
			return list[i].KeyID < list[j].KeyID
		}
		return list[i].CreatedAt.After(list[j].CreatedAt)
	})
	return list, nil
}

// ChangePassword 修改用户口令。
// bcrypt 计算全部在锁外执行；成功后强制失效该用户全部登录会话（口令可能已泄露），
// 并按 PasswordHistoryDepth 禁止重用最近若干次口令（等保三级 G-04）。
func (s *UserStore) ChangePassword(username, oldPassword, newPassword string) error {
	username = NormalizeUsername(username)

	s.mu.RLock()
	user, exists := s.users[username]
	var currentHash string
	var history []string
	if exists {
		currentHash = user.PasswordHash
		history = append([]string(nil), user.PasswordHistory...)
	}
	s.mu.RUnlock()
	if !exists {
		return ErrUserNotFound
	}
	if currentHash == "" {
		return ErrInvalidPassword
	}

	if err := bcrypt.CompareHashAndPassword([]byte(currentHash), []byte(oldPassword)); err != nil {
		s.recordFailedLogin(username)
		return ErrInvalidPassword
	}
	s.resetFailedLogin(username)

	if oldPassword == newPassword {
		return ErrPasswordSame
	}
	if err := ValidatePasswordStrength(newPassword, username); err != nil {
		return err
	}
	for _, h := range history {
		if h == "" {
			continue
		}
		if bcrypt.CompareHashAndPassword([]byte(h), []byte(newPassword)) == nil {
			return ErrPasswordReused
		}
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcryptCost)
	if err != nil {
		return fmt.Errorf("auth: hash password failed: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[username]
	if !ok {
		return ErrUserNotFound
	}

	newHistory := append([]string{u.PasswordHash}, u.PasswordHistory...)
	if len(newHistory) > PasswordHistoryDepth {
		newHistory = newHistory[:PasswordHistoryDepth]
	}
	now := time.Now().UTC()
	u.PasswordHistory = newHistory
	u.PasswordHash = string(hash)
	u.PasswordUpdatedAt = now
	u.UpdatedAt = now

	// 改密后强制全部会话下线（API Key 保留，机器账号可由管理员显式吊销）。
	s.revokeSessionsLocked(username)

	if err := s.saveToFileLocked(); err != nil {
		return err
	}
	s.version.bump()
	return nil
}

// Count 返回已注册用户总数。
func (s *UserStore) Count() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.users)
}

// ============================================================================
// 登录会话（内存态，不落盘）
// ============================================================================

// CreateSession 为已认证用户签发登录会话 Token（默认 24h，最长 24h）。
// 会话与 API Key 严格分离：不写入持久化文件、不出现在 /keys 列表中，进程重启即失效。
func (s *UserStore) CreateSession(username string, ttl time.Duration) (string, time.Time, error) {
	username = NormalizeUsername(username)
	if ttl <= 0 {
		ttl = DefaultSessionTTL
	}
	if ttl > MaxSessionTTL {
		ttl = MaxSessionTTL
	}

	token, err := GenerateSecureToken()
	if err != nil {
		return "", time.Time{}, fmt.Errorf("auth: generate session token failed: %w", err)
	}
	hash := HashToken(token)
	now := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	user, exists := s.users[username]
	if !exists {
		return "", time.Time{}, ErrUserNotFound
	}
	if user.Status != UserStatusActive {
		return "", time.Time{}, ErrUserDisabled
	}

	s.pruneExpiredSessionsLocked()
	if s.countUserSessionsLocked(username) >= MaxSessionsPerUser {
		s.evictOldestSessionLocked(username)
	}

	expiresAt := now.Add(ttl)
	s.sessions[hash] = &sessionEntry{
		Username:  username,
		Scopes:    append([]string(nil), user.Scopes...),
		CreatedAt: now,
		ExpiresAt: expiresAt,
	}
	s.version.bump()
	return token, expiresAt, nil
}

// RevokeSession 注销指定会话 Token（登出）。
func (s *UserStore) RevokeSession(token string) error {
	if token == "" {
		return ErrSessionNotFound
	}
	hash := HashToken(token)

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[hash]; !ok {
		return ErrSessionNotFound
	}
	delete(s.sessions, hash)
	s.version.bump()
	return nil
}

// RevokeUserSessions 强制下线指定用户的全部会话，返回被清理的会话数。
func (s *UserStore) RevokeUserSessions(username string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.revokeSessionsLocked(NormalizeUsername(username))
	if n > 0 {
		s.version.bump()
	}
	return n
}

func (s *UserStore) revokeSessionsLocked(username string) int {
	n := 0
	for hash, sess := range s.sessions {
		if sess.Username == username {
			delete(s.sessions, hash)
			n++
		}
	}
	return n
}

func (s *UserStore) pruneExpiredSessionsLocked() {
	now := time.Now()
	for hash, sess := range s.sessions {
		if !now.Before(sess.ExpiresAt) {
			delete(s.sessions, hash)
		}
	}
}

func (s *UserStore) countUserSessionsLocked(username string) int {
	n := 0
	for _, sess := range s.sessions {
		if sess.Username == username {
			n++
		}
	}
	return n
}

func (s *UserStore) evictOldestSessionLocked(username string) {
	var oldestHash string
	var oldestTime time.Time
	for hash, sess := range s.sessions {
		if sess.Username != username {
			continue
		}
		if oldestHash == "" || sess.CreatedAt.Before(oldestTime) {
			oldestHash, oldestTime = hash, sess.CreatedAt
		}
	}
	if oldestHash != "" {
		delete(s.sessions, oldestHash)
	}
}

// ============================================================================
// 活密钥快照（供认证内核使用）
// ============================================================================

// LiveHashedKeys 返回「HashToken(token) → KeyConfig」只读快照，包含有效 API Key 与登录会话。
// 直接挂载至 Settings.LiveInternalHashedKeys；仅在版本号变化时重建，其余请求零分配复用缓存。
//
// 快照为**只读共享**对象（调用方不得写入 map 或其中的 *KeyConfig）；重建时逐条深拷贝，
// 因此快照与存储内部状态不存在指针别名，密钥变更也不会就地修改已发出的旧快照。
func (s *UserStore) LiveHashedKeys() map[string]*KeyConfig {
	if s == nil {
		return nil
	}
	v := s.version.get()

	s.snapMu.Lock()
	defer s.snapMu.Unlock()
	if s.snapshotInit && s.snapshotVer == v && s.snapshot != nil {
		return s.snapshot
	}

	s.mu.RLock()
	snap := make(map[string]*KeyConfig, len(s.activeKeys)+len(s.sessions))
	for hash, entry := range s.activeKeys {
		if !entry.Active || entry.Config == nil || entry.Config.IsExpired() {
			continue
		}
		snap[hash] = entry.Config.Clone()
	}
	now := time.Now()
	for hash, sess := range s.sessions {
		if !now.Before(sess.ExpiresAt) {
			continue
		}
		exp := sess.ExpiresAt
		snap[hash] = &KeyConfig{
			Name:      sess.Username + ":session",
			Subject:   sess.Username,
			Scopes:    append([]string(nil), sess.Scopes...),
			ExpiresAt: &exp,
		}
	}
	s.mu.RUnlock()

	s.snapshot = snap
	s.snapshotVer = v
	s.snapshotInit = true
	return snap
}

// LiveHashedKeysFunc 返回可直接挂载至 Settings.LiveInternalHashedKeys 的回调函数。
func (s *UserStore) LiveHashedKeysFunc() func() map[string]*KeyConfig {
	return s.LiveHashedKeys
}

// Stats 返回存储规模快照（诊断用）。
func (s *UserStore) Stats() (users, keys, sessions int) {
	if s == nil {
		return 0, 0, 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.users), len(s.activeKeys), len(s.sessions)
}

// 编译期断言：UserStore 是「只落盘摘要」的活密钥来源。
var _ HashedLiveKeySource = (*UserStore)(nil)
