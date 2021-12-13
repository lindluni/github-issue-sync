package handlers

import (
	"context"
	"fmt"

	"github.com/google/go-github/v41/github"
	"github.com/lindluni/github-issue-sync/pkg/db"
	"github.com/lindluni/github-issue-sync/pkg/types"
	"github.com/shurcooL/githubv4"
	"github.com/sirupsen/logrus"
)

type EMU struct {
	Client        *github.Client
	DBClient      *db.Manager
	GitHubClient  *github.Client
	GraphQLClient *githubv4.Client

	Config *types.Config

	Logger *logrus.Logger
}

func (e *EMU) HandleIssue(webhook *types.WebHook) error {
	switch webhook.Action {
	case "opened":
		issue, err := e.openIssue(webhook)
		if err != nil {
			return err
		}
		err = e.DBClient.InsertIssueEntry(webhook, issue.GetNumber())
		if err != nil {
			return err
		}
	case "edited":
		err := e.editIssue(webhook)
		if err != nil {
			return err
		}
		err = e.DBClient.UpdateIssueEntry(webhook)
		if err != nil {
			return err
		}
	case "deleted":
		err := e.deleteIssue(webhook)
		if err != nil {
			return err
		}
		err = e.DBClient.DeleteIssueEntry(webhook)
		if err != nil {
			return err
		}
	case "closed":
		err := e.updateIssueState(webhook)
		if err != nil {
			return err
		}
		err = e.DBClient.UpdateIssueEntry(webhook)
		if err != nil {
			return err
		}
	case "reopened":
		err := e.updateIssueState(webhook)
		if err != nil {
			return err
		}
		err = e.DBClient.UpdateIssueEntry(webhook)
		if err != nil {
			return err
		}

	}
	return nil
}

func (e *EMU) openIssue(webhook *types.WebHook) (*github.Issue, error) {
	org := webhook.Repository.Owner.GetLogin()
	repo := webhook.Repository.GetName()
	issueNumber := webhook.Issue.GetNumber()
	title := webhook.Issue.GetTitle()
	author := webhook.Issue.User.GetLogin()
	body := webhook.Issue.GetBody()

	newTitle := fmt.Sprintf("%s/%s#%d: %s", org, repo, issueNumber, title)
	newBody := fmt.Sprintf("@%s posted:\n\n%s", author, body)

	issue, _, err := e.GitHubClient.Issues.Create(context.Background(), e.Config.Repo.Org, e.Config.Repo.Name, &github.IssueRequest{
		Title: &newTitle,
		Body:  &newBody,
	})

	return issue, err
}

func (e *EMU) editIssue(webhook *types.WebHook) error {
	org := webhook.Repository.Owner.GetLogin()
	repo := webhook.Repository.GetName()
	issueNumber := webhook.Issue.GetNumber()
	title := webhook.Issue.GetTitle()
	author := webhook.Issue.User.GetLogin()
	body := webhook.Issue.GetBody()

	githubIssueNumber, err := e.DBClient.GetGitHubIssueIDEntry(webhook)
	if err != nil {
		return err
	}

	newTitle := fmt.Sprintf("%s/%s#%d: %s", org, repo, issueNumber, title)
	newBody := fmt.Sprintf("@%s posted:\n\n%s", author, body)

	_, _, err = e.GitHubClient.Issues.Edit(context.Background(), e.Config.Repo.Org, e.Config.Repo.Name, githubIssueNumber, &github.IssueRequest{
		Title: &newTitle,
		Body:  &newBody,
	})

	return err
}

func (e *EMU) deleteIssue(webhook *types.WebHook) error {
	githubIssueNumber, err := e.DBClient.GetGitHubIssueIDEntry(webhook)
	if err != nil {
		return err
	}

	var mutation struct {
		DeleteIssue struct {
			Repository struct {
				ID string
			}
		} `graphql:"deleteIssue(input: $input)"`
	}

	issue, _, err := e.GitHubClient.Issues.Get(context.Background(), e.Config.Repo.Org, e.Config.Repo.Name, githubIssueNumber)
	if err != nil {
		return err
	}
	input := githubv4.DeleteIssueInput{
		IssueID: issue.GetNodeID(),
	}
	err = e.GraphQLClient.Mutate(context.Background(), &mutation, input, nil)
	if err != nil {
		return err
	}

	return nil
}

func (e *EMU) updateIssueState(webhook *types.WebHook) error {
	githubIssueNumber, err := e.DBClient.GetGitHubIssueIDEntry(webhook)
	if err != nil {
		return err
	}
	_, _, err = e.GitHubClient.Issues.Edit(context.Background(), e.Config.Repo.Org, e.Config.Repo.Name, githubIssueNumber, &github.IssueRequest{
		State: webhook.Issue.State,
	})

	return err
}

func (e *EMU) HandleIssueComment(webhook *types.WebHook) error {
	switch webhook.Action {
	case "created":
		id, err := e.createComment(webhook)
		if err != nil {
			return err
		}
		err = e.DBClient.InsertCommentEntry(webhook, id)
		if err != nil {
			return err
		}
	case "edited":
		err := e.editComment(webhook)
		if err != nil {
			return err
		}
		err = e.DBClient.UpdateCommentEntry(webhook)
		if err != nil {
			return err
		}
	case "deleted":
		err := e.deleteComment(webhook)
		if err != nil {
			return err
		}
		err = e.DBClient.DeleteCommentEntry(webhook)
		if err != nil {
			return err
		}
	}
	return nil
}

func (e *EMU) createComment(webhook *types.WebHook) (int64, error) {
	githubIssueNumber, err := e.DBClient.GetGitHubIssueIDEntry(webhook)
	if err != nil {
		return -1, err
	}
	author := webhook.Comment.User.GetLogin()
	body := webhook.Comment.GetBody()

	newBody := fmt.Sprintf("@%s posted:\n\n%s", author, body)

	comment, _, err := e.GitHubClient.Issues.CreateComment(context.Background(), e.Config.Repo.Org, e.Config.Repo.Name, githubIssueNumber, &github.IssueComment{
		Body: &newBody,
	})
	if err != nil {
		return -1, err
	}
	return comment.GetID(), nil
}

func (e *EMU) editComment(webhook *types.WebHook) error {
	githubIssueNumber, err := e.DBClient.GetGitHubCommentIDEntry(webhook)
	if err != nil {
		return err
	}
	author := webhook.Comment.User.GetLogin()
	body := webhook.Comment.GetBody()

	newBody := fmt.Sprintf("@%s posted:\n\n%s", author, body)
	_, _, err = e.GitHubClient.Issues.EditComment(context.Background(), e.Config.Repo.Org, e.Config.Repo.Name, int64(githubIssueNumber), &github.IssueComment{
		Body: &newBody,
	})
	if err != nil {
		return err
	}
	return nil
}

func (e *EMU) deleteComment(webhook *types.WebHook) error {
	githubIssueNumber, err := e.DBClient.GetGitHubCommentIDEntry(webhook)
	if err != nil {
		return err
	}

	_, err = e.GitHubClient.Issues.DeleteComment(context.Background(), e.Config.Repo.Org, e.Config.Repo.Name, int64(githubIssueNumber))
	if err != nil {
		return err
	}
	return nil
}
