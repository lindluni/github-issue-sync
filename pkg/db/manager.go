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
	_, err = m.Client.Exec("CREATE TABLE IF NOT EXISTS issue_sync.synced_comments (id int NOT NULL, issue_id int NOT NULL, synced_comment_id int, login VARCHAR(255), body TEXT, PRIMARY KEY (id), FOREIGN KEY (issue_id) REFERENCES issue_sync.issues(id) ON DELETE CASCADE)")
	if err != nil {
		return err
	}
	return nil
}

func (m *Manager) InsertIssueEntry(webhook *types.WebHook, syncedIssueNumber int) error {
	fmt.Println(webhook.Issue.GetID())
	_, err := m.Client.Exec("INSERT INTO issue_sync.issues (id, login, title, body, org, repo, issue_number, state, synced_issue_number) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)", webhook.Issue.GetID(), webhook.Issue.User.GetLogin(), webhook.Issue.GetTitle(), webhook.Issue.GetBody(), webhook.Repository.Owner.GetLogin(), webhook.Repository.GetName(), webhook.Issue.GetNumber(), webhook.Issue.GetState(), syncedIssueNumber)
	if err != nil {
		return err
	}
	return nil
}

func (m *Manager) InsertCommentEntry(webhook *types.WebHook, syncedCommentID int64) error {
	fmt.Println(webhook.Issue.GetID())
	_, err := m.Client.Exec("INSERT INTO issue_sync.comments (id, issue_id, login, body, synced_comment_id) VALUES (?, ?, ?, ?, ?)", webhook.Comment.GetID(), webhook.Issue.GetID(), webhook.Comment.User.GetLogin(), webhook.Comment.GetBody(), syncedCommentID)
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

func (m *Manager) UpdateSyncedCommentEntry(webhook *types.WebHook) error {
	_, err := m.Client.Exec("UPDATE issue_sync.synced_comments SET login = ?, body = ? WHERE id = ?", webhook.Comment.User.GetLogin(), webhook.Comment.GetBody(), webhook.Comment.GetID())
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

func (m *Manager) DeleteSyncedCommentEntry(webhook *types.WebHook) error {
	fmt.Println(webhook.Comment.GetID())
	_, err := m.Client.Exec("DELETE FROM issue_sync.synced_comments WHERE id = ?", webhook.Comment.GetID())
	if err != nil {
		return err
	}
	return nil
}

func (m *Manager) GetEMUIssueIDFromGitHubCommentEntry(webhook *types.WebHook) (string, string, int, error) {
	rows, err := m.Client.Query("SELECT issues.org, issues.repo, issues.issue_number FROM issue_sync.issues WHERE synced_issue_number = ? LIMIT 1", webhook.Issue.GetNumber())
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
	return "", "", -1, fmt.Errorf("unable to locate parent issues")
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

type EMUIssue struct {
	org         string
	repo        string
	issueNumber int64
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
