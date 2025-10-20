package repo

import (
	"context"
	"database/sql"

	"github.com/shainilps/relay/internal/model"
)

func CreateFundingUTXO(ctx context.Context, db *sql.DB, utxo *model.FundingUTXO) error {

	_, err := db.ExecContext(ctx, `INSERT INTO funding_utxos (utxo_id, tx_id, vout, locking_script) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return err
	}

	return nil
}

func CreateFundingUTXOs(ctx context.Context, db *sql.DB, utxos []model.FundingUTXO) error {

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `INSERT INTO funding_utxos (utxo_id, tx_id, vout, locking_script) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, utxo := range utxos {
		_, err := stmt.ExecContext(ctx, utxo.UtxoID, utxo.TxID, utxo.Vout, utxo.LockingScript)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

func GetAllUnspentFundingUTXOs(ctx context.Context, db *sql.DB) ([]model.UTXO, error) {

	utxos := make([]model.UTXO, 0)

	rows, err := db.QueryContext(ctx, `SELECT utxo_id, tx_id, vout, locking_script FROM funding_utxos WHERE spent_tx_id IS NULL`)
	if err != nil {
		return nil, err
	}

	for rows.Next() {
		var utxo model.UTXO
		err := rows.Scan(&utxo.UtxoID, &utxo.TxID, &utxo.Vout, &utxo.LockingScript)
		if err != nil {
			return nil, err
		}

		utxos = append(utxos, utxo)
	}

	return utxos, nil
}

func MarkFundingUTXOsAsSpent(ctx context.Context, db *sql.DB, spentTxID string, utxos []model.UTXO) error {

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `UPDATE funding_utxos SET spent_tx_id = ? WHERE utxo_id = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, utxo := range utxos {
		_, err := stmt.ExecContext(ctx, spentTxID, utxo.UtxoID)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

func CreateTransaction(ctx context.Context, db *sql.DB, transaction *model.Transaction) error {

	_, err := db.ExecContext(ctx, `INSERT INTO transactions (tx_id, tx_hex, network) VALUES (?, ?, ?)`, transaction.TxID, transaction.TxHex, transaction.Network)
	if err != nil {
		return err
	}

	return nil
}

func GetTransaction(ctx context.Context, db *sql.DB, txID *string) (*model.Transaction, error) {

	var transaction model.Transaction

	row := db.QueryRowContext(ctx, `SELECT tx_id, tx_hex, height, network, status, created_at FROM transactions WHERE tx_id = ? `, txID)
	err := row.Scan(&transaction.TxID, &transaction.TxHex, &transaction.Height, &transaction.Network, &transaction.Status, &transaction.CreatedAt)
	if err != nil {
		return nil, err
	}

	return &transaction, nil
}
