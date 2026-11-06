package index

import "database/sql"

func needsTextUpgrade(db Reader) (bool, error) {
	var count int
	err := db.QueryRow("SELECT count(*) FROM pragma_table_info('places') WHERE name='text'").Scan(&count)
	return count == 0, err
}

// Refresh calls this while holding the index lock. Readers keep the old chunks
// until each file is refreshed. A stopped refresh leaves old versions pending.
func upgradeText(db *sql.DB) error {
	needed, err := needsTextUpgrade(db)
	if err != nil || !needed {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("ALTER TABLE places ADD COLUMN text TEXT; DROP TRIGGER IF EXISTS chunks_update;" + chunkUpdateSQL + "UPDATE files SET chunker_version=0"); err != nil {
		return err
	}
	return tx.Commit()
}

func needsIdentityUpgrade(db Reader) (bool, error) {
	var count int
	err := db.QueryRow("SELECT count(*) FROM pragma_table_info('files') WHERE name='namespace'").Scan(&count)
	return count == 0, err
}

// The new chunk version makes every old file pending without discarding its text.
func upgradeIdentity(db *sql.DB) error {
	needed, err := needsIdentityUpgrade(db)
	if err != nil || !needed {
		return err
	}
	_, err = db.Exec("ALTER TABLE files ADD COLUMN namespace TEXT NOT NULL DEFAULT ''")
	return err
}
