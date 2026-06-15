package eval

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/mattn/go-sqlite3"

	"xhan-helper/internal/config"
)

// LoadDataset exports queries and corpus from the source database using the given strategy,
// and writes them into a SQLite eval.db at the specified output path.
func LoadDataset(cfg *config.Config, strategy ExportStrategy, projectName, outPath string) error {
	srcDB, err := sql.Open(cfg.DB.Driver, cfg.DB.DSN())
	if err != nil {
		return fmt.Errorf("open source db: %w", err)
	}
	defer srcDB.Close()

	if err := srcDB.Ping(); err != nil {
		return fmt.Errorf("ping source db: %w", err)
	}

	log.Printf("[eval] extracting queries with strategy=%q from %s@%s/%s",
		strategy.Name(), cfg.DB.Driver, cfg.DB.Host, cfg.DB.Database)

	queries, err := strategy.ExportQueries(context.Background(), srcDB)
	if err != nil {
		return fmt.Errorf("export queries: %w", err)
	}
	log.Printf("[eval] exported %d query candidates", len(queries))

	corpus, err := strategy.ExportCorpus(context.Background(), srcDB)
	if err != nil {
		return fmt.Errorf("export corpus: %w", err)
	}
	log.Printf("[eval] exported %d corpus entries", len(corpus))

	dir := filepath.Dir(outPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create output dir %q: %w", dir, err)
	}

	sqliteDB, err := sql.Open("sqlite3", outPath)
	if err != nil {
		return fmt.Errorf("open sqlite %q: %w", outPath, err)
	}
	defer sqliteDB.Close()

	if err := sqliteDB.Ping(); err != nil {
		return fmt.Errorf("ping sqlite: %w", err)
	}

	sqliteDB.SetMaxOpenConns(1)

	log.Printf("[eval] creating schema in %s", outPath)
	if err := createSchema(sqliteDB); err != nil {
		return err
	}

	if err := insertProject(sqliteDB, projectName); err != nil {
		return err
	}

	if err := insertQueries(sqliteDB, queries); err != nil {
		return err
	}
	log.Printf("[eval] inserted %d queries", len(queries))

	if err := insertCorpus(sqliteDB, corpus); err != nil {
		return err
	}
	log.Printf("[eval] inserted %d corpus entries", len(corpus))

	now := time.Now().UTC().Format(time.RFC3339)
	_ = insertMetadata(sqliteDB, "version", "v0.1")
	_ = insertMetadata(sqliteDB, "exported_at", now)
	_ = insertMetadata(sqliteDB, "strategy", strategy.Name())
	_ = insertMetadata(sqliteDB, "source_driver", cfg.DB.Driver)

	log.Printf("[eval] dataset %q created at %s (%d queries, %d corpus)",
		projectName, outPath, len(queries), len(corpus))

	return nil
}
