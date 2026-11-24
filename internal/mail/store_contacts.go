package mail

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ListContacts 按 owner 分页列出通信录；q 匹配姓名或邮箱子串。
func (s *Store) ListContacts(ctx context.Context, owners []string, q string, limit, offset int) ([]Contact, error) {
	if len(owners) == 0 {
		return nil, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	ownerPh, ownerArgs := ownerInClause(owners)
	args := append([]any{}, ownerArgs...)
	where := "owner_scope IN (" + ownerPh + ")"
	q = strings.TrimSpace(q)
	if q != "" {
		like := "%" + strings.ToLower(q) + "%"
		where += " AND (lower(name) LIKE ? OR lower(emails_json) LIKE ?)"
		args = append(args, like, like)
	}
	args = append(args, limit, offset)
	//nolint:gosec // G202: where 仅含固定片段与 ownerInClause 生成的 ? 占位符，值全在 args。
	rows, err := s.db.QueryContext(ctx, `SELECT id, owner_scope, revision, name, emails_json, created_at_utc, updated_at_utc
		FROM contacts WHERE `+where+` ORDER BY lower(name), id LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanContacts(rows)
}

// GetContact 授权读取。
func (s *Store) GetContact(ctx context.Context, id string, owners []string) (Contact, error) {
	c, err := s.loadContact(ctx, id)
	if err != nil {
		return Contact{}, err
	}
	if !ownerAllowed(c.OwnerScope, owners) {
		return Contact{}, ErrNotFound
	}
	return c, nil
}

// SaveContact 新建或 CAS 更新；邮箱全局按 owner 唯一（忽略大小写）。
func (s *Store) SaveContact(ctx context.Context, c Contact, expectedRevision int64) (Contact, error) {
	if c.OwnerScope == "" || strings.TrimSpace(c.Name) == "" {
		return Contact{}, fmt.Errorf("%w: name/owner", ErrInvalid)
	}
	emails := normalizeEmails(c.Emails)
	if len(emails) == 0 {
		return Contact{}, fmt.Errorf("%w: emails required", ErrInvalid)
	}
	c.Emails = emails
	c.Name = strings.TrimSpace(c.Name)
	now := time.Now().UTC()
	if c.ID == "" {
		c.ID = "contact-" + shortID(8)
		c.Revision = 1
		c.CreatedAtUTC = now
		c.UpdatedAtUTC = now
		if err := s.assertEmailsFree(ctx, c.OwnerScope, emails, ""); err != nil {
			return Contact{}, err
		}
		_, err := s.db.ExecContext(ctx, `INSERT INTO contacts(id, owner_scope, revision, name, emails_json, created_at_utc, updated_at_utc)
			VALUES (?,?,?,?,?,?,?)`, c.ID, c.OwnerScope, c.Revision, c.Name, encodeJSON(emails), fmtTime(c.CreatedAtUTC), fmtTime(c.UpdatedAtUTC))
		return c, err
	}
	if expectedRevision < 1 {
		return Contact{}, fmt.Errorf("%w: expectedRevision", ErrInvalid)
	}
	if err := s.assertEmailsFree(ctx, c.OwnerScope, emails, c.ID); err != nil {
		return Contact{}, err
	}
	c.Revision = expectedRevision + 1
	c.UpdatedAtUTC = now
	res, err := s.db.ExecContext(ctx, `UPDATE contacts SET revision=?, name=?, emails_json=?, updated_at_utc=?
		WHERE id=? AND owner_scope=? AND revision=?`,
		c.Revision, c.Name, encodeJSON(emails), fmtTime(c.UpdatedAtUTC), c.ID, c.OwnerScope, expectedRevision)
	if err != nil {
		return Contact{}, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return Contact{}, ErrConflict
	}
	return c, nil
}

// DeleteContact 删除联系人并从分组成员中移除。
func (s *Store) DeleteContact(ctx context.Context, id string, owners []string, expectedRevision int64) error {
	c, err := s.GetContact(ctx, id, owners)
	if err != nil {
		return err
	}
	if c.Revision != expectedRevision {
		return ErrConflict
	}
	// MaxOpenConns=1：事务外先读分组，避免 tx 占用连接时再 Query。
	groups, err := s.ListGroups(ctx, []string{c.OwnerScope}, 500, 0)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `DELETE FROM contacts WHERE id=? AND owner_scope=? AND revision=?`, id, c.OwnerScope, expectedRevision)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrConflict
	}
	for _, g := range groups {
		next := removeID(g.MemberIDs, id)
		if len(next) == len(g.MemberIDs) {
			continue
		}
		_, err = tx.ExecContext(ctx, `UPDATE groups_meta SET member_ids_json=?, revision=revision+1, updated_at_utc=? WHERE id=?`,
			encodeJSON(next), fmtTime(time.Now().UTC()), g.ID)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListGroups 分页列出分组。
func (s *Store) ListGroups(ctx context.Context, owners []string, limit, offset int) ([]Group, error) {
	if len(owners) == 0 {
		return nil, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	ownerPh, ownerArgs := ownerInClause(owners)
	args := append([]any{}, ownerArgs...)
	args = append(args, limit, offset)
	//nolint:gosec // G202: ownerPh 仅为 ? 占位符列表。
	rows, err := s.db.QueryContext(ctx, `SELECT id, owner_scope, revision, name, member_ids_json, created_at_utc, updated_at_utc
		FROM groups_meta WHERE owner_scope IN (`+ownerPh+`) ORDER BY lower(name), id LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Group
	for rows.Next() {
		var g Group
		var members, created, updated string
		if err := rows.Scan(&g.ID, &g.OwnerScope, &g.Revision, &g.Name, &members, &created, &updated); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(members), &g.MemberIDs)
		if g.MemberIDs == nil {
			g.MemberIDs = []string{}
		}
		g.CreatedAtUTC = parseTime(created)
		g.UpdatedAtUTC = parseTime(updated)
		out = append(out, g)
	}
	return out, rows.Err()
}

// SaveGroup 新建或 CAS 更新分组。
func (s *Store) SaveGroup(ctx context.Context, g Group, expectedRevision int64) (Group, error) {
	if g.OwnerScope == "" || strings.TrimSpace(g.Name) == "" {
		return Group{}, fmt.Errorf("%w: name/owner", ErrInvalid)
	}
	g.Name = strings.TrimSpace(g.Name)
	if g.MemberIDs == nil {
		g.MemberIDs = []string{}
	}
	now := time.Now().UTC()
	if g.ID == "" {
		g.ID = "group-" + shortID(8)
		g.Revision = 1
		g.CreatedAtUTC = now
		g.UpdatedAtUTC = now
		_, err := s.db.ExecContext(ctx, `INSERT INTO groups_meta(id, owner_scope, revision, name, member_ids_json, created_at_utc, updated_at_utc)
			VALUES (?,?,?,?,?,?,?)`, g.ID, g.OwnerScope, g.Revision, g.Name, encodeJSON(g.MemberIDs), fmtTime(g.CreatedAtUTC), fmtTime(g.UpdatedAtUTC))
		return g, err
	}
	if expectedRevision < 1 {
		return Group{}, fmt.Errorf("%w: expectedRevision", ErrInvalid)
	}
	g.Revision = expectedRevision + 1
	g.UpdatedAtUTC = now
	res, err := s.db.ExecContext(ctx, `UPDATE groups_meta SET revision=?, name=?, member_ids_json=?, updated_at_utc=?
		WHERE id=? AND owner_scope=? AND revision=?`,
		g.Revision, g.Name, encodeJSON(g.MemberIDs), fmtTime(g.UpdatedAtUTC), g.ID, g.OwnerScope, expectedRevision)
	if err != nil {
		return Group{}, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return Group{}, ErrConflict
	}
	return g, nil
}

// DeleteGroup 删除分组，不删联系人。
func (s *Store) DeleteGroup(ctx context.Context, id string, owners []string, expectedRevision int64) error {
	groups, err := s.ListGroups(ctx, owners, 500, 0)
	if err != nil {
		return err
	}
	var found *Group
	for i := range groups {
		if groups[i].ID == id {
			found = &groups[i]
			break
		}
	}
	if found == nil {
		return ErrNotFound
	}
	if found.Revision != expectedRevision {
		return ErrConflict
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM groups_meta WHERE id=? AND owner_scope=? AND revision=?`, id, found.OwnerScope, expectedRevision)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrConflict
	}
	return nil
}

// ContactImportRow 是 CSV 导入的一行候选。
type ContactImportRow struct {
	Name   string   `json:"name"`
	Emails []string `json:"emails"`
}

// ContactImportPreview 是导入预检结果；不落盘。
type ContactImportPreview struct {
	Accept []ContactImportRow `json:"accept"`
	Skip   []string           `json:"skip"` // 人读原因
}

// PreviewContactImport 校验 CSV 行，返回可提交项与重复/错误摘要；不落盘。
func (s *Store) PreviewContactImport(ctx context.Context, owner string, rows []ContactImportRow) (ContactImportPreview, error) {
	existing, err := s.ListContacts(ctx, []string{owner}, "", 500, 0)
	if err != nil {
		return ContactImportPreview{}, err
	}
	taken := map[string]struct{}{}
	for _, c := range existing {
		for _, e := range c.Emails {
			taken[strings.ToLower(e)] = struct{}{}
		}
	}
	var out ContactImportPreview
	seenBatch := map[string]struct{}{}
	for i, row := range rows {
		name := strings.TrimSpace(row.Name)
		emails := normalizeEmails(row.Emails)
		if name == "" || len(emails) == 0 {
			out.Skip = append(out.Skip, fmt.Sprintf("第%d行：姓名或邮箱缺失", i+1))
			continue
		}
		dup := false
		for _, e := range emails {
			if _, ok := taken[e]; ok {
				out.Skip = append(out.Skip, fmt.Sprintf("第%d行：邮箱 %s 已存在", i+1, e))
				dup = true
				break
			}
			if _, ok := seenBatch[e]; ok {
				out.Skip = append(out.Skip, fmt.Sprintf("第%d行：邮箱 %s 在导入文件内重复", i+1, e))
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		for _, e := range emails {
			seenBatch[e] = struct{}{}
		}
		out.Accept = append(out.Accept, ContactImportRow{Name: name, Emails: emails})
	}
	if out.Accept == nil {
		out.Accept = []ContactImportRow{}
	}
	if out.Skip == nil {
		out.Skip = []string{}
	}
	return out, nil
}

// CommitContactImport 提交预览中的 Accept 行。
func (s *Store) CommitContactImport(ctx context.Context, owner string, rows []ContactImportRow) ([]Contact, error) {
	preview, err := s.PreviewContactImport(ctx, owner, rows)
	if err != nil {
		return nil, err
	}
	var created []Contact
	for _, row := range preview.Accept {
		c, err := s.SaveContact(ctx, Contact{OwnerScope: owner, Name: row.Name, Emails: row.Emails}, 0)
		if err != nil {
			return created, err
		}
		created = append(created, c)
	}
	return created, nil
}

// ListDrafts 按更新时间倒序列出草稿摘要。
func (s *Store) ListDrafts(ctx context.Context, owners []string, limit, offset int) ([]Draft, error) {
	if len(owners) == 0 {
		return nil, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	ownerPh, ownerArgs := ownerInClause(owners)
	args := append([]any{}, ownerArgs...)
	args = append(args, limit, offset)
	//nolint:gosec // G202: ownerPh 仅为 ? 占位符列表。
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM drafts WHERE owner_scope IN (`+ownerPh+`)
		ORDER BY updated_at_utc DESC, id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close() // MaxOpenConns=1：必须先关游标再 loadDraft
	out := make([]Draft, 0, len(ids))
	for _, id := range ids {
		d, err := s.loadDraft(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

// CreateUserDraft 创建空用户草稿（非模型提案）。
func (s *Store) CreateUserDraft(ctx context.Context, owner, accountID string) (Draft, error) {
	now := time.Now().UTC()
	d := Draft{
		ID: "draft-" + shortID(10), OwnerScope: owner, AccountID: accountID,
		Revision: 1, Status: StatusDraft, SendMode: ModeTogether, DispatchMode: ModeImmediate,
		To: []MailAddress{}, Cc: []MailAddress{}, Bcc: []MailAddress{}, Attachments: []AttachmentRef{},
		CreatedAtUTC: now, UpdatedAtUTC: now,
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO drafts(
		id, owner_scope, account_id, revision, status, blocked_reason,
		source_session_id, source_turn_id, source_tool_use_id, source_proposal_md, source_created_at_utc,
		subject, body_markdown, to_json, cc_json, bcc_json, attachments_json,
		send_mode, dispatch_mode, scheduled_local, time_zone, scheduled_at_utc,
		created_at_utc, updated_at_utc
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		d.ID, d.OwnerScope, d.AccountID, d.Revision, d.Status, "",
		"", "", "", "", "",
		"", "", "[]", "[]", "[]", "[]",
		d.SendMode, d.DispatchMode, "", "", nil,
		fmtTime(d.CreatedAtUTC), fmtTime(d.UpdatedAtUTC),
	)
	return d, err
}

func (s *Store) loadContact(ctx context.Context, id string) (Contact, error) {
	var c Contact
	var emails, created, updated string
	err := s.db.QueryRowContext(ctx, `SELECT id, owner_scope, revision, name, emails_json, created_at_utc, updated_at_utc
		FROM contacts WHERE id=?`, id).Scan(&c.ID, &c.OwnerScope, &c.Revision, &c.Name, &emails, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Contact{}, ErrNotFound
	}
	if err != nil {
		return Contact{}, err
	}
	_ = json.Unmarshal([]byte(emails), &c.Emails)
	if c.Emails == nil {
		c.Emails = []string{}
	}
	c.CreatedAtUTC = parseTime(created)
	c.UpdatedAtUTC = parseTime(updated)
	return c, nil
}

func (s *Store) assertEmailsFree(ctx context.Context, owner string, emails []string, exceptID string) error {
	existing, err := s.ListContacts(ctx, []string{owner}, "", 500, 0)
	if err != nil {
		return err
	}
	for _, c := range existing {
		if c.ID == exceptID {
			continue
		}
		for _, e := range c.Emails {
			for _, want := range emails {
				if strings.EqualFold(e, want) {
					return fmt.Errorf("%w: email already used: %s", ErrInvalid, want)
				}
			}
		}
	}
	return nil
}

func normalizeEmails(in []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, raw := range in {
		a, err := NormalizeAddress("", raw)
		if err != nil {
			continue
		}
		if _, ok := seen[a.Address]; ok {
			continue
		}
		seen[a.Address] = struct{}{}
		out = append(out, a.Address)
	}
	return out
}

func ownerInClause(owners []string) (string, []any) {
	ph := make([]string, len(owners))
	args := make([]any, len(owners))
	for i, o := range owners {
		ph[i] = "?"
		args[i] = o
	}
	return strings.Join(ph, ","), args
}

func removeID(ids []string, id string) []string {
	out := ids[:0:0]
	for _, x := range ids {
		if x != id {
			out = append(out, x)
		}
	}
	return out
}

func scanContacts(rows *sql.Rows) ([]Contact, error) {
	var out []Contact
	for rows.Next() {
		var c Contact
		var emails, created, updated string
		if err := rows.Scan(&c.ID, &c.OwnerScope, &c.Revision, &c.Name, &emails, &created, &updated); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(emails), &c.Emails)
		if c.Emails == nil {
			c.Emails = []string{}
		}
		c.CreatedAtUTC = parseTime(created)
		c.UpdatedAtUTC = parseTime(updated)
		out = append(out, c)
	}
	return out, rows.Err()
}

func shortID(n int) string {
	if n <= 0 {
		n = 8
	}
	b := make([]byte, (n+1)/2)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	s := hex.EncodeToString(b)
	if len(s) > n {
		return s[:n]
	}
	return s
}
