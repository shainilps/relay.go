package repo

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/shainilps/relay/internal/model"
)

func CreateFundingUTXO(ctx context.Context, db *sql.DB, utxo *model.UTXO) error {

	_, err := db.ExecContext(ctx, `INSERT OR IGNORE INTO funding_utxos (utxo_id, tx_id, vout, amount) VALUES (?, ?, ?, ?)`, utxo.UtxoID, utxo.TxID, utxo.Vout, utxo.Amount)
	if err != nil {
		return err
	}
	return nil
}

func CreateFundingUTXOsIfNotExists(ctx context.Context, db *sql.DB, utxos []model.UTXO) error {

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO funding_utxos (utxo_id, tx_id, vout, amount) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, utxo := range utxos {
		_, err := stmt.ExecContext(ctx, utxo.UtxoID, utxo.TxID, utxo.Vout, utxo.Amount)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

func CreateFundingUTXOsIfNotExistsAndMarkAsSpent(ctx context.Context, db *sql.DB, utxos []model.UTXO) error {

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `INSERT INTO funding_utxos (utxo_id, tx_id, vout, amount, is_spent) VALUES (?, ?, ?, ?, true) ON CONFLICT(utxo_id) DO UPDATE SET is_spent = true`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, utxo := range utxos {
		_, err := stmt.ExecContext(ctx, utxo.UtxoID, utxo.TxID, utxo.Vout, utxo.Amount)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

func GetAllUnspentFundingUTXOs(ctx context.Context, db *sql.DB) ([]model.UTXO, error) {

	utxos := make([]model.UTXO, 0)

	rows, err := db.QueryContext(ctx, `SELECT utxo_id, tx_id, vout, amount FROM funding_utxos WHERE is_spent IS FALSE`)
	if err != nil {
		return nil, err
	}

	for rows.Next() {
		var utxo model.UTXO
		err := rows.Scan(&utxo.UtxoID, &utxo.TxID, &utxo.Vout, &utxo.Amount)
		if err != nil {
			return nil, err
		}

		utxos = append(utxos, utxo)
	}

	return utxos, nil
}

func MarkFundingUTXOsAsSpent(ctx context.Context, db *sql.DB, utxos []model.UTXO) error {

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `UPDATE funding_utxos SET is_spent = true WHERE utxo_id = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, utxo := range utxos {
		_, err := stmt.ExecContext(ctx, utxo.UtxoID)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

type DoubleSpendError struct {
	Outpoint model.Outpoint
	SpentBy  string
}

func (e *DoubleSpendError) Error() string {
	return fmt.Sprintf("double spend: %s:%d is already spent by tx %s", e.Outpoint.TxID, e.Outpoint.Vout, e.SpentBy)
}

func CreateTransaction(ctx context.Context, db *sql.DB, transaction *model.Transaction, inputs []model.Outpoint) error {

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `INSERT INTO transactions (tx_id, tx_hex, network, status, next_attempt_at) VALUES (?, ?, ?, ?, ?) ON CONFLICT(tx_id) DO NOTHING`,
		transaction.TxID, transaction.TxHex, transaction.Network, model.PENDING, transaction.NextAttemptAt)
	if err != nil {
		return err
	}

	claimStmt, err := tx.PrepareContext(ctx, `INSERT INTO tx_inputs (prev_tx_id, vout, tx_id) VALUES (?, ?, ?)
		ON CONFLICT(prev_tx_id, vout) DO UPDATE SET tx_id = excluded.tx_id, created_at = CURRENT_TIMESTAMP
		WHERE (SELECT status FROM transactions WHERE transactions.tx_id = tx_inputs.tx_id) = ?`)
	if err != nil {
		return err
	}
	defer claimStmt.Close()

	ownerStmt, err := tx.PrepareContext(ctx, `SELECT tx_id FROM tx_inputs WHERE prev_tx_id = ? AND vout = ?`)
	if err != nil {
		return err
	}
	defer ownerStmt.Close()

	for _, input := range inputs {
		_, err := claimStmt.ExecContext(ctx, input.TxID, input.Vout, transaction.TxID, model.FAILED)
		if err != nil {
			return err
		}

		var owner string
		err = ownerStmt.QueryRowContext(ctx, input.TxID, input.Vout).Scan(&owner)
		if err != nil {
			return err
		}
		if owner != transaction.TxID {
			return &DoubleSpendError{Outpoint: input, SpentBy: owner}
		}
	}

	return tx.Commit()
}

const transactionColumns = `tx_id, tx_hex, network, status, attempts, last_broadcast_at, next_attempt_at, COALESCE(block_hash, ''), COALESCE(block_height, 0), COALESCE(last_error, ''), CAST(strftime('%s', created_at) AS INTEGER)`

func scanTransaction(row interface{ Scan(...any) error }) (*model.Transaction, error) {
	var transaction model.Transaction
	var lastBroadcastAt sql.NullInt64

	err := row.Scan(&transaction.TxID, &transaction.TxHex, &transaction.Network, &transaction.Status, &transaction.Attempts,
		&lastBroadcastAt, &transaction.NextAttemptAt, &transaction.BlockHash, &transaction.BlockHeight, &transaction.LastError, &transaction.CreatedAt)
	if err != nil {
		return nil, err
	}

	if lastBroadcastAt.Valid {
		transaction.LastBroadcastAt = &lastBroadcastAt.Int64
	}

	return &transaction, nil
}

func GetTransaction(ctx context.Context, db *sql.DB, txID string) (*model.Transaction, error) {

	row := db.QueryRowContext(ctx, `SELECT `+transactionColumns+` FROM transactions WHERE tx_id = ?`, txID)
	return scanTransaction(row)
}

func GetDueTransactions(ctx context.Context, db *sql.DB, now int64, limit int) ([]model.Transaction, error) {

	rows, err := db.QueryContext(ctx, `SELECT `+transactionColumns+` FROM transactions WHERE status IN (?, ?) AND next_attempt_at <= ? ORDER BY rowid LIMIT ?`,
		model.PENDING, model.BROADCASTED, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	transactions := make([]model.Transaction, 0)
	for rows.Next() {
		transaction, err := scanTransaction(rows)
		if err != nil {
			return nil, err
		}
		transactions = append(transactions, *transaction)
	}

	return transactions, rows.Err()
}

func MarkBroadcasted(ctx context.Context, db *sql.DB, txID string, now int64, nextAttemptAt int64) error {

	_, err := db.ExecContext(ctx, `UPDATE transactions SET status = ?, attempts = attempts + 1, last_broadcast_at = ?, next_attempt_at = ?, last_error = NULL, updated_at = CURRENT_TIMESTAMP WHERE tx_id = ?`,
		model.BROADCASTED, now, nextAttemptAt, txID)
	return err
}

func RecordBroadcastError(ctx context.Context, db *sql.DB, txID string, errMsg string, nextAttemptAt int64) error {

	_, err := db.ExecContext(ctx, `UPDATE transactions SET attempts = attempts + 1, last_error = ?, next_attempt_at = ?, updated_at = CURRENT_TIMESTAMP WHERE tx_id = ?`,
		errMsg, nextAttemptAt, txID)
	return err
}

func RecordUnreachable(ctx context.Context, db *sql.DB, txID string, errMsg string, nextAttemptAt int64) error {

	_, err := db.ExecContext(ctx, `UPDATE transactions SET last_error = ?, next_attempt_at = ?, updated_at = CURRENT_TIMESTAMP WHERE tx_id = ?`,
		errMsg, nextAttemptAt, txID)
	return err
}

func MarkSynced(ctx context.Context, db *sql.DB, txID string, blockHash string, blockHeight uint64) error {

	_, err := db.ExecContext(ctx, `UPDATE transactions SET status = ?, block_hash = ?, block_height = ?, last_error = NULL, updated_at = CURRENT_TIMESTAMP WHERE tx_id = ?`,
		model.SYNCED, blockHash, blockHeight, txID)
	return err
}

func MarkFailed(ctx context.Context, db *sql.DB, txID string, errMsg string) error {

	_, err := db.ExecContext(ctx, `UPDATE transactions SET status = ?, last_error = ?, updated_at = CURRENT_TIMESTAMP WHERE tx_id = ?`,
		model.FAILED, errMsg, txID)
	return err
}
