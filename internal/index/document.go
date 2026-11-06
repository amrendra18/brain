package index

import "strings"

// DocumentText reads each occurrence in order, including its saved formatting.
func DocumentText(db Reader, file int64) (string, error) {
	legacy, err := needsTextUpgrade(db)
	if err != nil {
		return "", err
	}
	column := "COALESCE(places.text,chunks.text)"
	if legacy {
		column = "chunks.text"
	}
	rows, err := db.Query("SELECT "+column+" FROM places JOIN chunks ON chunks.id=places.chunk WHERE places.file=? ORDER BY places.ordinal", file)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	texts := []string{}
	for rows.Next() {
		var text string
		if err = rows.Scan(&text); err != nil {
			return "", err
		}
		texts = append(texts, text)
	}
	return strings.Join(texts, "\n\n"), rows.Err()
}
