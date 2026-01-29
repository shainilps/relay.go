package db

import (
	"database/sql"

	_ "github.com/mattn/go-sqlite3"
	"github.com/spf13/viper"
)

func NewClient() (*sql.DB, error) {

	//default
	path := "./data/database.db"

	if viper.GetString("db.path") != "" {
		path = viper.GetString("db.path")
	}

	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}

	if err := db.Ping(); err != nil {
		return nil, err
	}

	return db, nil
}
