package db

import (
	"database/sql"
	"errors"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/spf13/viper"
)

func NewClient() (*sql.DB, error) {

	url := viper.GetString("db.url")
	if url == "" {
		return nil, errors.New("db.url is not set")
	}

	db, err := sql.Open("pgx", url)
	if err != nil {
		return nil, err
	}

	if err := db.Ping(); err != nil {
		return nil, err
	}

	return db, nil
}
