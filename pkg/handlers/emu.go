package handlers

import (
	"context"
	"fmt"

	"github.com/google/go-github/v41/github"
	"github.com/lindluni/github-issue-sync/pkg/db"
	"github.com/lindluni/github-issue-sync/pkg/types"
	"github.com/shurcooL/githubv4"
)

type EMU struct{}

func (e *EMU) HandleIssue(webhook *types.WebHook, dbClient *db.Manager, githubClient *github.Client, graphQLClient *githubv4.Client, config *types.Config) error {
	switch webhook.Action {
	case "opened":
		issue, err := e.openIssue(webhook, githubClient, config)
		if err != nil {
			return err
		}
		err = dbClient.InsertIssueEntry(webhook, issue.GetNumber())
		if err != nil {
			return err
		}
	case "edited":
		err := e.editIssue(webhook, dbClient, githubClient, config)
		if err != nil {
			return err
		}
		err = dbClient.UpdateIssueEntry(webhook)
		if err != nil {
			return err
		}
	case "deleted":
		err := e.deleteIssue(webhook, dbClient, githubClient, graphQLClient, config)
		if err != nil {
			return err
		}
		err = dbClient.DeleteIssueEntry(webhook)
		if err != nil {
			return err
		}
	case "closed":
		err := e.updateIssueState(webhook, dbClient, githubClient, config)
		if err != nil {
			return err
		}
		err = dbClient.UpdateIssueEntry(webhook)
		if err != nil {
			return err
		}
	case "reopened":
		err := e.updateIssueState(webhook, dbClient, githubClient, config)
		if err != nil {
			return err
		}
		err = dbClient.UpdateIssueEntry(webhook)
		if err != nil {
			return err
		}

	}
	return nil
}

func (e *EMU) openIssue(webhook *types.WebHook, client *github.Client, config *types.Config) (*github.Issue, error) {
	org := webhook.Repository.Owner.GetLogin()
	repo := webhook.Repository.GetName()
	issueNumber := webhook.Issue.GetNumber()
	title := webhook.Issue.GetTitle()
	author := webhook.Issue.User.GetLogin()
	body := webhook.Issue.GetBody()

	newTitle := fmt.Sprintf("%s/%s#%d: %s", org, repo, issueNumber, title)
	newBody := fmt.Sprintf("@%s posted:\n\n%s", author, body)

	issue, _, err := client.Issues.Create(context.Background(), config.Repo.Org, config.Repo.Name, &github.IssueRequest{
		Title: &newTitle,
		Body:  &newBody,
	})

	return issue, err
}

func (e *EMU) editIssue(webhook *types.WebHook, dbClient *db.Manager, githubClient *github.Client, config *types.Config) error {
	org := webhook.Repository.Owner.GetLogin()
	repo := webhook.Repository.GetName()
	issueNumber := webhook.Issue.GetNumber()
	title := webhook.Issue.GetTitle()
	author := webhook.Issue.User.GetLogin()
	body := webhook.Issue.GetBody()

	githubIssueNumber, err := dbClient.GetGitHubIssueIDEntry(webhook)
	if err != nil {
		return err
	}

	newTitle := fmt.Sprintf("%s/%s#%d: %s", org, repo, issueNumber, title)
	newBody := fmt.Sprintf("@%s posted:\n\n%s", author, body)

	_, _, err = githubClient.Issues.Edit(context.Background(), config.Repo.Org, config.Repo.Name, githubIssueNumber, &github.IssueRequest{
		Title: &newTitle,
		Body:  &newBody,
	})

	return err
}

func (e *EMU) deleteIssue(webhook *types.WebHook, dbClient *db.Manager, githubClient *github.Client, graphQLClient *githubv4.Client, config *types.Config) error {
	githubIssueNumber, err := dbClient.GetGitHubIssueIDEntry(webhook)
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

	issue, _, err := githubClient.Issues.Get(context.Background(), config.Repo.Org, config.Repo.Name, githubIssueNumber)
	if err != nil {
		return err
	}
	input := githubv4.DeleteIssueInput{
		IssueID: issue.GetNodeID(),
	}
	err = graphQLClient.Mutate(context.Background(), &mutation, input, nil)
	if err != nil {
		return err
	}

	return nil
}

func (e *EMU) updateIssueState(webhook *types.WebHook, dbClient *db.Manager, githubClient *github.Client, config *types.Config) error {
	githubIssueNumber, err := dbClient.GetGitHubIssueIDEntry(webhook)
	if err != nil {
		return err
	}
	_, _, err = githubClient.Issues.Edit(context.Background(), config.Repo.Org, config.Repo.Name, githubIssueNumber, &github.IssueRequest{
		State: webhook.Issue.State,
	})

	return err
}

func (e *EMU) HandleIssueComment(webhook *types.WebHook, dbClient *db.Manager, githubClient *github.Client, config *types.Config) error {
	switch webhook.Action {
	case "created":
		id, err := e.createComment(webhook, dbClient, githubClient, config)
		if err != nil {
			return err
		}
		err = dbClient.InsertCommentEntry(webhook, id)
		if err != nil {
			return err
		}
	case "edited":
		err := e.editComment(webhook, dbClient, githubClient, config)
		if err != nil {
			return err
		}
		err = dbClient.UpdateCommentEntry(webhook)
		if err != nil {
			return err
		}
	case "deleted":
		err := e.deleteComment(webhook, dbClient, githubClient, config)
		if err != nil {
			return err
		}
		err = dbClient.DeleteCommentEntry(webhook)
		if err != nil {
			return err
		}
	}
	return nil
}

func (e *EMU) createComment(webhook *types.WebHook, dbClient *db.Manager, githubClient *github.Client, config *types.Config) (int64, error) {
	githubIssueNumber, err := dbClient.GetGitHubIssueIDEntry(webhook)
	if err != nil {
		return -1, err
	}
	author := webhook.Comment.User.GetLogin()
	body := webhook.Comment.GetBody()

	newBody := fmt.Sprintf("@%s posted:\n\n%s", author, body)

	comment, _, err := githubClient.Issues.CreateComment(context.Background(), config.Repo.Org, config.Repo.Name, githubIssueNumber, &github.IssueComment{
		Body: &newBody,
	})
	if err != nil {
		return -1, err
	}
	return comment.GetID(), nil
}

func (e *EMU) editComment(webhook *types.WebHook, dbClient *db.Manager, githubClient *github.Client, config *types.Config) error {
	githubIssueNumber, err := dbClient.GetGitHubCommentIDEntry(webhook)
	if err != nil {
		return err
	}
	author := webhook.Comment.User.GetLogin()
	body := webhook.Comment.GetBody()

	newBody := fmt.Sprintf("@%s posted:\n\n%s", author, body)
	_, _, err = githubClient.Issues.EditComment(context.Background(), config.Repo.Org, config.Repo.Name, int64(githubIssueNumber), &github.IssueComment{
		Body: &newBody,
	})
	if err != nil {
		return err
	}
	return nil
}

func (e *EMU) deleteComment(webhook *types.WebHook, dbClient *db.Manager, githubClient *github.Client, config *types.Config) error {
	githubIssueNumber, err := dbClient.GetGitHubCommentIDEntry(webhook)
	if err != nil {
		return err
	}

	_, err = githubClient.Issues.DeleteComment(context.Background(), config.Repo.Org, config.Repo.Name, int64(githubIssueNumber))
	if err != nil {
		return err
	}
	return nil
}
