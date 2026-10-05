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

	_, err := tx.ExecContext(ctx, `INSERT INTO transactions (tx_id, tx_hex, status, next_attempt_at) VALUES ($1, $2, $3, $4) ON CONFLICT (tx_id) DO NOTHING`,
		transaction.TxID, transaction.TxHex, model.PENDING, transaction.NextAttemptAt)
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

	rows, err := db.QueryContext(ctx, `SELECT queue_utxos.utxo_id, queue_utxos.tx_id, queue_utxos.vout, queue_utxos.amount, queue_utxos.queue FROM queue_utxos
		JOIN transactions ON transactions.tx_id = queue_utxos.tx_id
		WHERE queue_utxos.published IS FALSE AND transactions.status != $1 ORDER BY queue_utxos.seq`, model.FAILED)
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

func IsTransactionFailed(ctx context.Context, db *sql.DB, txID string) (bool, error) {

	var failed bool
	err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM transactions WHERE tx_id = $1 AND status = $2)`, txID, model.FAILED).Scan(&failed)
	return failed, err
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

const transactionColumns = `tx_id, tx_hex, status, attempts, last_broadcast_at, next_attempt_at, COALESCE(block_hash, ''), COALESCE(block_height, 0), COALESCE(last_error, ''), EXTRACT(EPOCH FROM created_at)::BIGINT`

func scanTransaction(row interface{ Scan(...any) error }) (*model.Transaction, error) {
	var transaction model.Transaction
	var lastBroadcastAt sql.NullInt64

	err := row.Scan(&transaction.TxID, &transaction.TxHex, &transaction.Status, &transaction.Attempts,
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

func ClaimDueTransactions(ctx context.Context, db *sql.DB, now int64, leaseUntil int64, limit int) ([]model.Transaction, error) {

	rows, err := db.QueryContext(ctx, `WITH due AS (
			SELECT tx_id FROM transactions
			WHERE status IN ($1, $2) AND next_attempt_at <= $3
			ORDER BY seq LIMIT $4
			FOR UPDATE SKIP LOCKED
		), claimed AS (
			UPDATE transactions SET next_attempt_at = $5 FROM due
			WHERE transactions.tx_id = due.tx_id
			RETURNING transactions.*
		)
		SELECT `+transactionColumns+` FROM claimed ORDER BY seq`,
		model.PENDING, model.BROADCASTED, now, limit, leaseUntil)
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

func ReleaseClaims(ctx context.Context, db *sql.DB, txIDs []string, nextAttemptAt int64) error {

	_, err := db.ExecContext(ctx, `UPDATE transactions SET next_attempt_at = $1 WHERE tx_id = ANY($2) AND status IN ($3, $4)`,
		nextAttemptAt, txIDs, model.PENDING, model.BROADCASTED)
	return err
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

func MarkFailed(ctx context.Context, db *sql.DB, txID string, errMsg string) ([]string, error) {

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `UPDATE transactions SET status = $1, last_error = $2, updated_at = now() WHERE tx_id = $3`,
		model.FAILED, errMsg, txID)
	if err != nil {
		return nil, err
	}

	rows, err := tx.QueryContext(ctx, `WITH RECURSIVE descendants AS (
			SELECT tx_id FROM tx_inputs WHERE prev_tx_id = $1
			UNION
			SELECT tx_inputs.tx_id FROM tx_inputs JOIN descendants ON tx_inputs.prev_tx_id = descendants.tx_id
		)
		UPDATE transactions SET status = $2, last_error = $3, updated_at = now()
		WHERE tx_id IN (SELECT tx_id FROM descendants) AND status IN ($4, $5)
		RETURNING tx_id`,
		txID, model.FAILED, fmt.Sprintf("parent tx %s failed", txID), model.PENDING, model.BROADCASTED)
	if err != nil {
		return nil, err
	}

	cascaded := make([]string, 0)
	for rows.Next() {
		var descendant string
		if err := rows.Scan(&descendant); err != nil {
			rows.Close()
			return nil, err
		}
		cascaded = append(cascaded, descendant)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	failed := append([]string{txID}, cascaded...)
	for _, failedTxID := range failed {
		_, err = tx.ExecContext(ctx, `UPDATE funding_utxos SET is_spent = true WHERE tx_id = $1`, failedTxID)
		if err != nil {
			return nil, err
		}
	}

	return cascaded, tx.Commit()
}

const (
	RecoveryFee     = "fee"
	RecoveryFunding = "funding"
)

type RecoveryCandidate struct {
	Kind       string
	Outpoint   model.Outpoint
	FailedTxID string
}

func GetRecoveryCandidates(ctx context.Context, db *sql.DB, limit int) ([]RecoveryCandidate, error) {

	rows, err := db.QueryContext(ctx, `
		SELECT $1::TEXT, queue_utxos.tx_id, queue_utxos.vout, tx_inputs.tx_id FROM queue_utxos
		JOIN tx_inputs ON tx_inputs.prev_tx_id = queue_utxos.tx_id AND tx_inputs.vout = queue_utxos.vout
		JOIN transactions spender ON spender.tx_id = tx_inputs.tx_id AND spender.status = $3
		JOIN transactions creator ON creator.tx_id = queue_utxos.tx_id AND creator.status != $3
		WHERE queue_utxos.chain_spent IS FALSE
		UNION ALL
		SELECT $2::TEXT, funding_utxos.tx_id, funding_utxos.vout, tx_inputs.tx_id FROM funding_utxos
		JOIN tx_inputs ON tx_inputs.prev_tx_id = funding_utxos.tx_id AND tx_inputs.vout = funding_utxos.vout
		JOIN transactions spender ON spender.tx_id = tx_inputs.tx_id AND spender.status = $3
		LEFT JOIN transactions creator ON creator.tx_id = funding_utxos.tx_id
		WHERE funding_utxos.chain_spent IS FALSE AND funding_utxos.is_spent IS TRUE AND (creator.status IS NULL OR creator.status != $3)
		LIMIT $4`,
		RecoveryFee, RecoveryFunding, model.FAILED, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	candidates := make([]RecoveryCandidate, 0)
	for rows.Next() {
		var candidate RecoveryCandidate
		err := rows.Scan(&candidate.Kind, &candidate.Outpoint.TxID, &candidate.Outpoint.Vout, &candidate.FailedTxID)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, candidate)
	}

	return candidates, rows.Err()
}

func RecoverUtxo(ctx context.Context, db *sql.DB, candidate RecoveryCandidate) (bool, error) {

	restore := `UPDATE queue_utxos SET published = false`
	table := "queue_utxos"
	if candidate.Kind == RecoveryFunding {
		restore = `UPDATE funding_utxos SET is_spent = false`
		table = "funding_utxos"
	}

	result, err := db.ExecContext(ctx, `WITH released AS (
			DELETE FROM tx_inputs WHERE prev_tx_id = $1 AND vout = $2 AND tx_id = $3 RETURNING 1
		)
		`+restore+` WHERE `+table+`.tx_id = $1 AND `+table+`.vout = $2 AND EXISTS (SELECT 1 FROM released)`,
		candidate.Outpoint.TxID, candidate.Outpoint.Vout, candidate.FailedTxID)
	if err != nil {
		return false, err
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func MarkChainSpent(ctx context.Context, db *sql.DB, candidate RecoveryCandidate) error {

	table := "queue_utxos"
	if candidate.Kind == RecoveryFunding {
		table = "funding_utxos"
	}

	_, err := db.ExecContext(ctx, `UPDATE `+table+` SET chain_spent = true WHERE tx_id = $1 AND vout = $2`,
		candidate.Outpoint.TxID, candidate.Outpoint.Vout)
	return err
}

func GetFundingBalance(ctx context.Context, db *sql.DB) (int64, int64, error) {

	var balance, count int64
	err := db.QueryRowContext(ctx, `SELECT COALESCE(SUM(amount), 0), COUNT(*) FROM funding_utxos WHERE is_spent IS FALSE AND chain_spent IS FALSE`).Scan(&balance, &count)
	return balance, count, err
}

func CountTransactionsByStatus(ctx context.Context, db *sql.DB) (map[model.TransactionStatus]int64, error) {

	rows, err := db.QueryContext(ctx, `SELECT status, COUNT(*) FROM transactions GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := map[model.TransactionStatus]int64{model.PENDING: 0, model.BROADCASTED: 0, model.SYNCED: 0, model.FAILED: 0}
	for rows.Next() {
		var status model.TransactionStatus
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		counts[status] = count
	}

	return counts, rows.Err()
}

func CountUnpublishedQueueUTXOs(ctx context.Context, db *sql.DB) (int64, error) {

	var count int64
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM queue_utxos
		JOIN transactions ON transactions.tx_id = queue_utxos.tx_id
		WHERE queue_utxos.published IS FALSE AND transactions.status != $1`, model.FAILED).Scan(&count)
	return count, err
}
