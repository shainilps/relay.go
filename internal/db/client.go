package db

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/shainilps/relay/internal/model"
	"github.com/spf13/viper"
)

//go:embed migrations/*.sql
var migrations embed.FS

const MIGRATION_LOCK_ID = 7426591

func SchemaFor(network model.Network) string {
	return "relay_" + strings.ToLower(string(network))
}

func NewClient(network model.Network) (*sql.DB, error) {

	rawURL := viper.GetString("db.url")
	if rawURL == "" {
		return nil, errors.New("db.url is not set")
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	schema := SchemaFor(network)
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()

	db, err := sql.Open("pgx", parsed.String())
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return nil, err
	}

	if _, err := db.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS `+schema); err != nil {
		return nil, fmt.Errorf("create schema %s: %w", schema, err)
	}

	if err := Migrate(ctx, db); err != nil {
		return nil, fmt.Errorf("migrate schema %s: %w", schema, err)
	}

	return db, nil
}

func Migrate(ctx context.Context, db *sql.DB) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, MIGRATION_LOCK_ID); err != nil {
		return err
	}
	defer conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, MIGRATION_LOCK_ID)

	files, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return err
	}

	provider, err := goose.NewProvider(goose.DialectPostgres, db, files)
	if err != nil {
		return err
	}

	_, err = provider.Up(ctx)
	return err
}
