package mediaaccess

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// accountRow is one user_media_server_accounts row: what Cantinarr did on a
// media server for a user. It is an action log; the server is the live truth.
type accountRow struct {
	UserID             int64
	InstanceID         string
	RemoteUserID       string
	RemoteUsername     string
	CreatedByCantinarr bool
	CreatedAt          time.Time
	DisabledAt         sql.NullTime
}

const accountColumns = "user_id, instance_id, remote_user_id, remote_username, created_by_cantinarr, created_at, disabled_at"

var (
	errAccountConflict = errors.New("account row already exists")
	errRemoteConflict  = errors.New("remote account already linked")
	errRowReference    = errors.New("account row references a missing user or instance")
)

func scanAccount(scanner interface{ Scan(dest ...any) error }) (accountRow, error) {
	var row accountRow
	if err := scanner.Scan(
		&row.UserID, &row.InstanceID, &row.RemoteUserID, &row.RemoteUsername,
		&row.CreatedByCantinarr, &row.CreatedAt, &row.DisabledAt,
	); err != nil {
		return accountRow{}, err
	}
	return row, nil
}

func (s *Service) getAccount(userID int64, instanceID string) (*accountRow, error) {
	row, err := scanAccount(s.db.QueryRow(
		"SELECT "+accountColumns+" FROM user_media_server_accounts WHERE user_id = ? AND instance_id = ?",
		userID, instanceID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get media server account: %w", err)
	}
	return &row, nil
}

func (s *Service) listAccountsForUser(userID int64) ([]accountRow, error) {
	rows, err := s.db.Query(
		"SELECT "+accountColumns+" FROM user_media_server_accounts WHERE user_id = ? ORDER BY instance_id",
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("list media server accounts: %w", err)
	}
	defer rows.Close()
	var out []accountRow
	for rows.Next() {
		row, err := scanAccount(rows)
		if err != nil {
			return nil, fmt.Errorf("scan media server account: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// listAccounts returns every row joined with its instance, for the admin
// Users screen. Instance name and type ride along so the app never has to
// join against a possibly stale instance list.
func (s *Service) listAccounts() ([]Account, error) {
	rows, err := s.db.Query(
		`SELECT a.user_id, a.instance_id, si.name, si.service_type, a.remote_user_id, a.remote_username,
		        a.created_by_cantinarr, a.created_at, a.disabled_at
		 FROM user_media_server_accounts a
		 JOIN service_instances si ON si.id = a.instance_id
		 ORDER BY a.user_id, si.sort_order, si.name, si.id`,
	)
	if err != nil {
		return nil, fmt.Errorf("list media server accounts: %w", err)
	}
	defer rows.Close()
	out := []Account{}
	for rows.Next() {
		var a Account
		var disabledAt sql.NullTime
		if err := rows.Scan(
			&a.UserID, &a.InstanceID, &a.InstanceName, &a.ServiceType, &a.RemoteUserID, &a.Username,
			&a.CreatedByCantinarr, &a.CreatedAt, &disabledAt,
		); err != nil {
			return nil, fmt.Errorf("scan media server account: %w", err)
		}
		a.Disabled = disabledAt.Valid
		out = append(out, a)
	}
	return out, rows.Err()
}

// insertAccount records a new row. With requireGrant the insert is guarded by
// the grant in the same statement, so a grant revoked while the remote
// account was being created leaves no row (inserted=false) and the caller
// rolls the remote account back. Conflicts are classified so the service can
// answer precisely without parsing SQLite text at every call site.
func (s *Service) insertAccount(row accountRow, requireGrant bool) (inserted bool, err error) {
	var res sql.Result
	if requireGrant {
		res, err = s.db.Exec(
			`INSERT INTO user_media_server_accounts (user_id, instance_id, remote_user_id, remote_username, created_by_cantinarr)
			 SELECT ?, ?, ?, ?, ?
			 WHERE EXISTS (SELECT 1 FROM user_instance_grants WHERE user_id = ? AND instance_id = ?)`,
			row.UserID, row.InstanceID, row.RemoteUserID, row.RemoteUsername, row.CreatedByCantinarr,
			row.UserID, row.InstanceID,
		)
	} else {
		res, err = s.db.Exec(
			`INSERT INTO user_media_server_accounts (user_id, instance_id, remote_user_id, remote_username, created_by_cantinarr)
			 VALUES (?, ?, ?, ?, ?)`,
			row.UserID, row.InstanceID, row.RemoteUserID, row.RemoteUsername, row.CreatedByCantinarr,
		)
	}
	if err != nil {
		msg := err.Error()
		switch {
		case strings.Contains(msg, "user_media_server_accounts.user_id"):
			return false, errAccountConflict
		case strings.Contains(msg, "user_media_server_accounts.instance_id"):
			return false, errRemoteConflict
		case strings.Contains(msg, "FOREIGN KEY"):
			return false, errRowReference
		}
		return false, fmt.Errorf("insert media server account: %w", err)
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func (s *Service) setDisabledAt(userID int64, instanceID string, disabled bool) error {
	var stamp any
	if disabled {
		stamp = time.Now().UTC()
	}
	if _, err := s.db.Exec(
		"UPDATE user_media_server_accounts SET disabled_at = ? WHERE user_id = ? AND instance_id = ?",
		stamp, userID, instanceID,
	); err != nil {
		return fmt.Errorf("stamp media server account: %w", err)
	}
	return nil
}

func (s *Service) deleteAccount(userID int64, instanceID string) (bool, error) {
	res, err := s.db.Exec(
		"DELETE FROM user_media_server_accounts WHERE user_id = ? AND instance_id = ?",
		userID, instanceID,
	)
	if err != nil {
		return false, fmt.Errorf("delete media server account: %w", err)
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// listDriftedAccountUsers returns the users whose recorded account state
// disagrees with their grants: Cantinarr decided to switch an account off (or
// back on) and the write to the media server never landed, so the row still
// carries the old stamp. It compares rows against grants only, never against
// the live server, so an account an admin disabled on the server side is left
// alone rather than fought over on a timer.
func (s *Service) listDriftedAccountUsers() ([]int64, error) {
	rows, err := s.db.Query(
		`SELECT DISTINCT a.user_id
		 FROM user_media_server_accounts a
		 LEFT JOIN user_instance_grants g
		   ON g.user_id = a.user_id AND g.instance_id = a.instance_id
		 WHERE (g.instance_id IS NULL AND a.disabled_at IS NULL)
		    OR (g.instance_id IS NOT NULL AND a.disabled_at IS NOT NULL)
		 ORDER BY a.user_id`,
	)
	if err != nil {
		return nil, fmt.Errorf("list drifted media server accounts: %w", err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var userID int64
		if err := rows.Scan(&userID); err != nil {
			return nil, fmt.Errorf("scan drifted media server account: %w", err)
		}
		out = append(out, userID)
	}
	return out, rows.Err()
}

// listAccountsCreatedOn returns the accounts Cantinarr itself created on one
// instance. Linked accounts are excluded on purpose: Cantinarr never edits a
// policy it did not write.
func (s *Service) listAccountsCreatedOn(instanceID string) ([]accountRow, error) {
	rows, err := s.db.Query(
		"SELECT "+accountColumns+
			" FROM user_media_server_accounts WHERE instance_id = ? AND created_by_cantinarr = 1 ORDER BY user_id",
		instanceID,
	)
	if err != nil {
		return nil, fmt.Errorf("list media server accounts: %w", err)
	}
	defer rows.Close()
	var out []accountRow
	for rows.Next() {
		row, err := scanAccount(rows)
		if err != nil {
			return nil, fmt.Errorf("scan media server account: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
