package repo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/shainilps/relay/internal/model"
)

func CreateFundingUTXOsIfNotExists(ctx context.Context, db *sql.DB, utxos []model.UTXO) error {

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `INSERT INTO funding_utxos (utxo_id, tx_id, vout, amount) VALUES ($1, $2, $3, $4) ON CONFLICT (utxo_id) DO NOTHING`)
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
	defer rows.Close()

	for rows.Next() {
		var utxo model.UTXO
		err := rows.Scan(&utxo.UtxoID, &utxo.TxID, &utxo.Vout, &utxo.Amount)
		if err != nil {
			return nil, err
		}

		utxos = append(utxos, utxo)
	}

	return utxos, rows.Err()
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

	err = createTransaction(ctx, tx, transaction, inputs)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func createTransaction(ctx context.Context, tx *sql.Tx, transaction *model.Transaction, inputs []model.Outpoint) error {

	_, err := tx.ExecContext(ctx, `INSERT INTO transactions (tx_id, tx_hex, network, status, next_attempt_at) VALUES ($1, $2, $3, $4, $5) ON CONFLICT (tx_id) DO NOTHING`,
		transaction.TxID, transaction.TxHex, transaction.Network, model.PENDING, transaction.NextAttemptAt)
	if err != nil {
		return err
	}

	claimStmt, err := tx.PrepareContext(ctx, `INSERT INTO tx_inputs (prev_tx_id, vout, tx_id) VALUES ($1, $2, $3)
		ON CONFLICT (prev_tx_id, vout) DO UPDATE SET tx_id = excluded.tx_id, created_at = now()
		WHERE (SELECT status FROM transactions WHERE transactions.tx_id = tx_inputs.tx_id) = $4`)
	if err != nil {
		return err
	}
	defer claimStmt.Close()

	ownerStmt, err := tx.PrepareContext(ctx, `SELECT tx_id FROM tx_inputs WHERE prev_tx_id = $1 AND vout = $2`)
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

	return nil
}

func StoreFundingTransaction(ctx context.Context, db *sql.DB, transaction *model.Transaction, spent []model.UTXO, queueUtxos []model.QueueUTXO, change *model.UTXO) error {

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	inputs := make([]model.Outpoint, 0, len(spent))
	for _, utxo := range spent {
		inputs = append(inputs, model.Outpoint{TxID: utxo.TxID, Vout: utxo.Vout})
	}

	err = createTransaction(ctx, tx, transaction, inputs)
	if err != nil {
		return err
	}

	for _, utxo := range spent {
		_, err := tx.ExecContext(ctx, `UPDATE funding_utxos SET is_spent = true WHERE utxo_id = $1`, utxo.UtxoID)
		if err != nil {
			return err
		}
	}

	for _, utxo := range queueUtxos {
		_, err := tx.ExecContext(ctx, `INSERT INTO queue_utxos (utxo_id, tx_id, vout, amount, queue) VALUES ($1, $2, $3, $4, $5) ON CONFLICT (utxo_id) DO NOTHING`,
			utxo.UtxoID, utxo.TxID, utxo.Vout, utxo.Amount, utxo.Queue)
		if err != nil {
			return err
		}
	}

	if change != nil {
		_, err := tx.ExecContext(ctx, `INSERT INTO funding_utxos (utxo_id, tx_id, vout, amount) VALUES ($1, $2, $3, $4) ON CONFLICT (utxo_id) DO NOTHING`,
			change.UtxoID, change.TxID, change.Vout, change.Amount)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

func GetUnpublishedQueueUTXOs(ctx context.Context, db *sql.DB) ([]model.QueueUTXO, error) {

	rows, err := db.QueryContext(ctx, `SELECT utxo_id, tx_id, vout, amount, queue FROM queue_utxos WHERE published IS FALSE ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	utxos := make([]model.QueueUTXO, 0)
	for rows.Next() {
		var utxo model.QueueUTXO
		err := rows.Scan(&utxo.UtxoID, &utxo.TxID, &utxo.Vout, &utxo.Amount, &utxo.Queue)
		if err != nil {
			return nil, err
		}
		utxos = append(utxos, utxo)
	}

	return utxos, rows.Err()
}

func MarkQueueUTXOPublished(ctx context.Context, db *sql.DB, utxoID string) error {

	_, err := db.ExecContext(ctx, `UPDATE queue_utxos SET published = true WHERE utxo_id = $1`, utxoID)
	return err
}

func GetSpendingTransaction(ctx context.Context, db *sql.DB, outpoint model.Outpoint) (string, error) {

	var txID string
	err := db.QueryRowContext(ctx, `SELECT tx_inputs.tx_id FROM tx_inputs JOIN transactions ON transactions.tx_id = tx_inputs.tx_id WHERE tx_inputs.prev_tx_id = $1 AND tx_inputs.vout = $2 AND transactions.status != $3`,
		outpoint.TxID, outpoint.Vout, model.FAILED).Scan(&txID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}

	return txID, nil
}

const transactionColumns = `tx_id, tx_hex, network, status, attempts, last_broadcast_at, next_attempt_at, COALESCE(block_hash, ''), COALESCE(block_height, 0), COALESCE(last_error, ''), EXTRACT(EPOCH FROM created_at)::BIGINT`

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

	row := db.QueryRowContext(ctx, `SELECT `+transactionColumns+` FROM transactions WHERE tx_id = $1`, txID)
	return scanTransaction(row)
}

func GetDueTransactions(ctx context.Context, db *sql.DB, now int64, limit int) ([]model.Transaction, error) {

	rows, err := db.QueryContext(ctx, `SELECT `+transactionColumns+` FROM transactions WHERE status IN ($1, $2) AND next_attempt_at <= $3 ORDER BY seq LIMIT $4`,
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

	_, err := db.ExecContext(ctx, `UPDATE transactions SET status = $1, attempts = attempts + 1, last_broadcast_at = $2, next_attempt_at = $3, last_error = NULL, updated_at = now() WHERE tx_id = $4`,
		model.BROADCASTED, now, nextAttemptAt, txID)
	return err
}

func RecordBroadcastError(ctx context.Context, db *sql.DB, txID string, errMsg string, nextAttemptAt int64) error {

	_, err := db.ExecContext(ctx, `UPDATE transactions SET attempts = attempts + 1, last_error = $1, next_attempt_at = $2, updated_at = now() WHERE tx_id = $3`,
		errMsg, nextAttemptAt, txID)
	return err
}

func RecordUnreachable(ctx context.Context, db *sql.DB, txID string, errMsg string, nextAttemptAt int64) error {

	_, err := db.ExecContext(ctx, `UPDATE transactions SET last_error = $1, next_attempt_at = $2, updated_at = now() WHERE tx_id = $3`,
		errMsg, nextAttemptAt, txID)
	return err
}

func MarkSynced(ctx context.Context, db *sql.DB, txID string, blockHash string, blockHeight uint64) error {

	_, err := db.ExecContext(ctx, `UPDATE transactions SET status = $1, block_hash = $2, block_height = $3, last_error = NULL, updated_at = now() WHERE tx_id = $4`,
		model.SYNCED, blockHash, blockHeight, txID)
	return err
}

func MarkFailed(ctx context.Context, db *sql.DB, txID string, errMsg string) error {

	_, err := db.ExecContext(ctx, `UPDATE transactions SET status = $1, last_error = $2, updated_at = now() WHERE tx_id = $3`,
		model.FAILED, errMsg, txID)
	return err
}
