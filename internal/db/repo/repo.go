package repo

import (
	"context"
	"database/sql"

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

func CreateTransaction(ctx context.Context, db *sql.DB, transaction *model.Transaction) error {

	_, err := db.ExecContext(ctx, `INSERT INTO transactions (tx_id, tx_hex, network) VALUES (?, ?, ?)`, transaction.TxID, transaction.TxHex, transaction.Network)
	if err != nil {
		return err
	}

	return nil
}

func GetTransaction(ctx context.Context, db *sql.DB, txID *string) (*model.Transaction, error) {

	var transaction model.Transaction

	row := db.QueryRowContext(ctx, `SELECT tx_id, tx_hex, height, network, status  FROM transactions WHERE tx_id = ? `, txID)
	err := row.Scan(&transaction.TxID, &transaction.TxHex, &transaction.Height, &transaction.Network, &transaction.Status)
	if err != nil {
		return nil, err
	}

	return &transaction, nil
}
