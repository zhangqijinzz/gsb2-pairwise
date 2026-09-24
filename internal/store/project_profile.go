package store

import (
	"database/sql"
	"strings"
	"time"
)

type ProjectProfile struct {
	RepoPath    string `json:"repoPath"`
	CommitHash  string `json:"commitHash"`
	ProfileText string `json:"profileText"`
	CreatedAt   int64  `json:"createdAt"`
	UpdatedAt   int64  `json:"updatedAt"`
}

func (s *Store) GetProjectProfile(repoPath, commitHash string) (*ProjectProfile, error) {
	repoPath = strings.TrimSpace(repoPath)
	commitHash = strings.TrimSpace(commitHash)
	if repoPath == "" || commitHash == "" {
		return nil, nil
	}

	var profile ProjectProfile
	err := s.DB.QueryRow(`
SELECT repo_path, commit_hash, profile_text, created_at, updated_at
FROM project_profiles
WHERE repo_path = ? AND commit_hash = ?`,
		repoPath, commitHash,
	).Scan(&profile.RepoPath, &profile.CommitHash, &profile.ProfileText, &profile.CreatedAt, &profile.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &profile, nil
}

func (s *Store) UpsertProjectProfile(repoPath, commitHash, profileText string) error {
	repoPath = strings.TrimSpace(repoPath)
	commitHash = strings.TrimSpace(commitHash)
	profileText = strings.TrimSpace(profileText)
	if repoPath == "" || commitHash == "" || profileText == "" {
		return nil
	}

	now := time.Now().Unix()
	_, err := s.DB.Exec(`
INSERT INTO project_profiles (repo_path, commit_hash, profile_text, created_at, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(repo_path, commit_hash) DO UPDATE SET
    profile_text = excluded.profile_text,
    updated_at = excluded.updated_at`,
		repoPath, commitHash, profileText, now, now,
	)
	return err
}
