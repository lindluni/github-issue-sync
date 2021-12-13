package db

import (
	"database/sql"
	"fmt"

	_ "github.com/go-sql-driver/mysql"
	"github.com/lindluni/github-issue-sync/pkg/types"
)

type Manager struct {
	Client *sql.DB
}

func (m *Manager) InitDB() error {
	_, err := m.Client.Exec("CREATE DATABASE IF NOT EXISTS issue_sync")
	if err != nil {
		return err
	}
	_, err = m.Client.Exec("CREATE TABLE IF NOT EXISTS issue_sync.issues (id int NOT NULL, login VARCHAR(255), title VARCHAR(255), body TEXT, org VARCHAR(255), repo VARCHAR(255), issue_number int, state VARCHAR(255), synced_issue_number int, PRIMARY KEY (id))")
	if err != nil {
		return err
	}
	_, err = m.Client.Exec("CREATE TABLE IF NOT EXISTS issue_sync.comments (id int NOT NULL, issue_id int NOT NULL, synced_comment_id int, login VARCHAR(255), body TEXT, PRIMARY KEY (id), FOREIGN KEY (issue_id) REFERENCES issue_sync.issues(id) ON DELETE CASCADE)")
	if err != nil {
		return err
	}

	return nil
}

func (m *Manager) InsertIssueEntry(webhook *types.WebHook, syncedIssueNumber int) error {
	_, err := m.Client.Exec("INSERT INTO issue_sync.issues (id, login, title, body, org, repo, issue_number, state, synced_issue_number) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)", webhook.Issue.GetID(), webhook.Issue.User.GetLogin(), webhook.Issue.GetTitle(), webhook.Issue.GetBody(), webhook.Repository.Owner.GetLogin(), webhook.Repository.GetName(), webhook.Issue.GetNumber(), webhook.Issue.GetState(), syncedIssueNumber)
	if err != nil {
		return err
	}
	return nil
}

func (m *Manager) InsertCommentEntry(webhook *types.WebHook, syncedCommentID int64) error {
	_, err := m.Client.Exec("INSERT INTO issue_sync.comments (id, issue_id, login, body, synced_comment_id) VALUES (?, ?, ?, ?, ?)", webhook.Comment.GetID(), webhook.Issue.GetID(), webhook.Comment.User.GetLogin(), webhook.Comment.GetBody(), syncedCommentID)
	if err != nil {
		return err
	}
	return nil
}

func (m *Manager) InsertGitHubCommentEntry(webhook *types.WebHook, emuIssueId, syncedCommentID int64) error {
	_, err := m.Client.Exec("INSERT INTO issue_sync.comments (id, issue_id, login, body, synced_comment_id) VALUES (?, ?, ?, ?, ?)", webhook.Comment.GetID(), emuIssueId, webhook.Comment.User.GetLogin(), webhook.Comment.GetBody(), syncedCommentID)
	if err != nil {
		return err
	}
	return nil
}

func (m *Manager) UpdateIssueEntry(webhook *types.WebHook) error {
	_, err := m.Client.Exec("UPDATE issue_sync.issues SET login = ?, title = ?, body = ?, state = ? WHERE id = ?", webhook.Issue.User.GetLogin(), webhook.Issue.GetTitle(), webhook.Issue.GetBody(), webhook.Issue.GetState(), webhook.Issue.GetID())
	if err != nil {
		return err
	}
	return nil
}

func (m *Manager) UpdateCommentEntry(webhook *types.WebHook) error {
	_, err := m.Client.Exec("UPDATE issue_sync.comments SET login = ?, body = ? WHERE id = ?", webhook.Comment.User.GetLogin(), webhook.Comment.GetBody(), webhook.Comment.GetID())
	if err != nil {
		return err
	}
	return nil
}

func (m *Manager) DeleteIssueEntry(webhook *types.WebHook) error {
	_, err := m.Client.Exec("DELETE FROM issue_sync.issues WHERE id = ?", webhook.Issue.GetID())
	if err != nil {
		return err
	}
	return nil
}

func (m *Manager) DeleteCommentEntry(webhook *types.WebHook) error {
	_, err := m.Client.Exec("DELETE FROM issue_sync.comments WHERE id = ?", webhook.Comment.GetID())
	if err != nil {
		return err
	}
	return nil
}

func (m *Manager) GetEMUIssueIDFromGitHubCommentEntry(webhook *types.WebHook) (int64, string, string, int, error) {
	rows, err := m.Client.Query("SELECT issue_sync.issues.id, issue_sync.issues.org, issue_sync.issues.repo, issue_sync.issues.issue_number FROM issue_sync.issues WHERE synced_issue_number = ? LIMIT 1", webhook.Issue.GetNumber())
	if err != nil {
		return -1, "", "", -1, err
	}
	var org, repo string
	var id int64
	var issueNumber int
	if rows.Next() {
		err = rows.Scan(&id, &org, &repo, &issueNumber)
		if err != nil {
			return -1, "", "", -1, err
		}
		return id, org, repo, issueNumber, nil
	}
	return -1, "", "", -1, fmt.Errorf("unable to locate parent issues")
}

func (m *Manager) GetGitHubIssueIDEntry(webhook *types.WebHook) (int, error) {
	rows, err := m.Client.Query("SELECT synced_issue_number FROM issue_sync.issues WHERE id = ? LIMIT 1", webhook.Issue.GetID())
	if err != nil {
		return -1, err
	}
	var id int
	if rows.Next() {
		err = rows.Scan(&id)
		if err != nil {
			return -1, err
		}
		return id, nil
	}
	return -1, fmt.Errorf("unable to locate parent issues")
}

func (m *Manager) GetGitHubCommentIDEntry(webhook *types.WebHook) (int, error) {
	rows, err := m.Client.Query("SELECT synced_comment_id FROM issue_sync.comments WHERE id = ? LIMIT 1", webhook.Comment.GetID())
	if err != nil {
		return -1, err
	}
	var id int
	if rows.Next() {
		err = rows.Scan(&id)
		if err != nil {
			return -1, err
		}
		return id, nil
	}
	return -1, fmt.Errorf("unable to locate comment id")
}

func (m *Manager) GetEMUCommentIDEntry(webhook *types.WebHook) (string, string, int64, error) {
	rows, err := m.Client.Query("SELECT issue_sync.issues.org, issue_sync.issues.repo, issue_sync.comments.synced_comment_id FROM issue_sync.issues, issue_sync.comments WHERE issue_sync.issues.id = issue_sync.comments.issue_id AND issue_sync.comments.id = ? LIMIT 1", webhook.Comment.GetID())
	if err != nil {
		return "", "", -1, err
	}
	var org, repo string
	var id int64
	if rows.Next() {
		err = rows.Scan(&org, &repo, &id)
		if err != nil {
			return "", "", -1, err
		}
		return org, repo, id, nil
	}
	return "", "", -1, fmt.Errorf("unable to locate comment id")
}

func (m *Manager) GetEMUIssue(webhook *types.WebHook) (string, string, int, error) {
	rows, err := m.Client.Query("SELECT issue_sync.issues.org, issue_sync.issues.repo, issue_sync.issues.issue_number FROM issue_sync.issues WHERE issue_sync.issues.synced_issue_number = ? LIMIT 1", webhook.Issue.GetNumber())
	if err != nil {
		return "", "", -1, err
	}
	var org, repo string
	var issueNumber int
	if rows.Next() {
		err = rows.Scan(&org, &repo, &issueNumber)
		if err != nil {
			return "", "", -1, err
		}
		return org, repo, issueNumber, nil
	}
	return "", "", -1, fmt.Errorf("unable to locate org")
}
