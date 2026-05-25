package store

import "database/sql"

type FileHistory struct {
	Path          string
	CommitCount   int
	FirstCommitAt int64
	LastCommitAt  int64
	AuthorCount   int
	LastAuthor    string
	LastSubject   string
}

func (s *Store) UpsertFileHistory(h FileHistory) error {
	_, err := s.db.Exec(`
		INSERT INTO file_history (path, commit_count, first_commit_at, last_commit_at, author_count, last_author, last_subject)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			commit_count = excluded.commit_count,
			first_commit_at = excluded.first_commit_at,
			last_commit_at = excluded.last_commit_at,
			author_count = excluded.author_count,
			last_author = excluded.last_author,
			last_subject = excluded.last_subject`,
		h.Path, h.CommitCount, h.FirstCommitAt, h.LastCommitAt, h.AuthorCount, h.LastAuthor, h.LastSubject)
	return err
}

func (s *Store) GetFileHistory(path string) (*FileHistory, error) {
	row := s.db.QueryRow(`
		SELECT path, commit_count, first_commit_at, last_commit_at, author_count, last_author, last_subject
		FROM file_history WHERE path = ?`, path)
	var h FileHistory
	if err := row.Scan(&h.Path, &h.CommitCount, &h.FirstCommitAt, &h.LastCommitAt,
		&h.AuthorCount, &h.LastAuthor, &h.LastSubject); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return &h, nil
}
