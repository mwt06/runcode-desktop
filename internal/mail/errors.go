package mail

import "errors"

var (
	// ErrConflict 表示乐观锁修订不匹配。
	ErrConflict = errors.New("mail: revision conflict")
	// ErrNotFound 对越权或不存在的 ID 统一返回，避免泄露外属数据。
	ErrNotFound = errors.New("mail: not found")
	// ErrInvalid 表示字段/模式校验失败。
	ErrInvalid = errors.New("mail: invalid input")
	// ErrConfirmationExpired 表示确认令牌过期或已使用。
	ErrConfirmationExpired = errors.New("mail: confirmation expired or used")
	// ErrQuotaExceeded 表示本机单账号日额度不足。
	ErrQuotaExceeded = errors.New("mail: daily quota exceeded")
	// ErrSchemaTooNew 表示库 schema 比本客户端新，禁止删库重建。
	ErrSchemaTooNew = errors.New("mail: database schema newer than supported")
)
