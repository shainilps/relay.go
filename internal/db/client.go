package db

import (
	"database/sql"
	"log"

	_ "github.com/mattn/go-sqlite3"
)

func NewClient() *sql.DB {
	db, err := sql.Open("sqlite3", "./.db")
	if err != nil {
		log.Fatalf("db connection error: %v", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatalf("db connecton error: %v", err)
	}

	log.Println("Connected to SQLite database!")
	return db
}
